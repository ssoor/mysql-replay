package stream

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bobguo/mysql-replay/stats"
	"github.com/bobguo/mysql-replay/util"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/reassembly"
)

type MySQLEvent struct {
	Conn     ConnID              `json:"conn"`
	Time     time.Time           `json:"time"`
	Type     util.MysqlEventType `json:"type"`
	StmtID   string              `json:"stmtID,omitempty"`
	Params   []interface{}       `json:"params,omitempty"`
	DB       string              `json:"db,omitempty"`
	Username string              `json:"username,omitempty"`
	Query    string              `json:"query,omitempty"`
	Pr       *PacketRes          `json:"packet_res,omitempty"`
	Rr       *ReplayRes          `json:"replay_res,omitempty"`
}

func (event *MySQLEvent) Reset(params []interface{}) *MySQLEvent {
	// event.Time = 0
	event.Type = 0
	event.StmtID = ""
	event.Params = params
	event.DB = ""
	event.Query = ""
	return event
}

func (event *MySQLEvent) NewReplayRes() {
	rr := new(ReplayRes)
	rr.ErrNO = 0
	rr.ErrDesc = ""
	rr.Values = rr.Values[0:0]
	rr.ColumnNum = 0
	rr.ColNames = rr.ColNames[0:0]
	rr.ColValues = rr.ColValues[0:0][0:0]
	rr.SqlStatment = ""
	rr.SqlBeginTime = 0
	rr.SqlEndTime = 0

	event.Rr = rr
}

func (event *MySQLEvent) String() string {
	conn := event.Conn.HashStr()
	switch event.Type {
	case util.EventQuery:
		return fmt.Sprintf("%s execute {query:%q} @ %d", conn, formatQuery(event.Query), event.Time)
	case util.EventStmtExecute:
		return fmt.Sprintf("%s execute stmt {id:%s,params:%v} @%d", conn, event.StmtID, event.Params, event.Time)
	case util.EventStmtPrepare:
		return fmt.Sprintf("%s prepare stmt {id:%s,query:%q} @%d", conn, event.StmtID, formatQuery(event.Query), event.Time)
	case util.EventStmtClose:
		return fmt.Sprintf("%s close stmt {id:%s} @%d", conn, event.StmtID, event.Time)
	case util.EventHandshake:
		return fmt.Sprintf("%s connect {username:%q,db:%q} @%d", conn, event.Username, event.DB, event.Time)
	case util.EventQuit:
		return fmt.Sprintf("%s quit @%d", conn, event.Time)
	default:
		return fmt.Sprintf("%s unknown event {type:%v} @%d", conn, event.Type, event.Time)
	}
}

func formatQuery(query string) string {
	if len(query) > 1024 {
		query = query[:700] + "..." + query[len(query)-300:]
	}
	return query
}

const (
	sep = '\t'

	typeI64 = byte('i')
	typeU64 = byte('u')
	typeF32 = byte('f')
	typeF64 = byte('d')
	typeStr = byte('s')
	typeBin = byte('b')
	typeNil = byte('0')
	typeLst = byte('[')
)

func AppendEvent(buf []byte, event MySQLEvent) ([]byte, error) {
	var err error
	buf = strconv.AppendInt(buf, event.Time.UnixNano(), 10)
	buf = append(buf, sep)
	buf = strconv.AppendUint(buf, uint64(event.Type), 10)
	switch event.Type {
	case util.EventQuery:
		buf = append(buf, sep)
		buf = strconv.AppendQuote(buf, event.Query)
	case util.EventStmtExecute:
		buf = append(buf, sep)
		buf = []byte(event.StmtID)
		buf = append(buf, sep)
		buf, err = AppendStmtParams(buf, event.Params)
		if err != nil {
			return nil, err
		}
	case util.EventStmtPrepare:
		buf = append(buf, sep)
		buf = []byte(event.StmtID)
		buf = append(buf, sep)
		buf = strconv.AppendQuote(buf, event.Query)
	case util.EventStmtClose:
		buf = append(buf, sep)
		buf = []byte(event.StmtID)
	case util.EventHandshake:
		buf = append(buf, sep)
		buf = strconv.AppendQuote(buf, event.DB)
	case util.EventQuit:
	default:
		return nil, fmt.Errorf("unknown event type: %v", event.Type)
	}
	return buf, nil
}

func AppendStmtParams(buf []byte, params []interface{}) ([]byte, error) {
	s := len(buf) + 1
	buf = append(buf, typeLst)
	buf = append(buf, make([]byte, len(params))...)
	for i, param := range params {
		if param == nil {
			buf[s+i] = typeNil
			buf = append(buf, sep)
			buf = append(buf, []byte("nil")...)
			continue
		}
		switch x := param.(type) {
		case int64:
			buf[s+i] = typeI64
			buf = append(buf, sep)
			buf = strconv.AppendInt(buf, x, 10)
		case uint64:
			buf[s+i] = typeU64
			buf = append(buf, sep)
			buf = strconv.AppendUint(buf, x, 10)
		case string:
			buf[s+i] = typeStr
			buf = append(buf, sep)
			buf = strconv.AppendQuote(buf, x)
		case float32:
			buf[s+i] = typeF32
			buf = append(buf, sep)
			buf = strconv.AppendFloat(buf, float64(x), 'g', -1, 32)
		case float64:
			buf[s+i] = typeF64
			buf = append(buf, sep)
			buf = strconv.AppendFloat(buf, x, 'g', -1, 64)
		case []byte:
			buf[s+i] = typeBin
			buf = append(buf, sep)
			buf = strconv.AppendQuote(buf, hex.EncodeToString(x))
		default:
			return nil, fmt.Errorf("unsupported param type: %T", param)
		}
	}
	return buf, nil
}

func ScanEvent(s string, pos int, event *MySQLEvent) (int, error) {
	var (
		posNext int
		err     error
	)
	// time
	if len(s) < pos+1 {
		return pos, fmt.Errorf("scan time of event from an empty string")
	}
	posNext = nextSep(s, pos)
	timestamp, err := strconv.ParseInt(s[pos:posNext], 10, 64)
	if err != nil {
		return pos, fmt.Errorf("scan time of event from (%s): %v", s[pos:posNext], err)
	}
	event.Time = time.Unix(timestamp/int64(time.Nanosecond), timestamp%int64(time.Nanosecond))
	pos = posNext + 1
	// type
	if len(s) < pos+1 {
		return pos, fmt.Errorf("scan type of event from an empty string")
	}
	posNext = nextSep(s, pos)
	eventType, err := strconv.ParseUint(s[pos:posNext], 10, 64)
	if err != nil {
		return pos, fmt.Errorf("scan type of event from (%s): %v", s[pos:posNext], err)
	}
	event.Type = util.MysqlEventType(eventType)
	pos = posNext + 1

	switch event.Type {
	case util.EventQuery:
		// query
		if len(s) < pos+1 {
			return pos, fmt.Errorf("scan query of event from an empty string")
		}
		posNext = nextSep(s, pos)
		event.Query, err = strconv.Unquote(s[pos:posNext])
		if err != nil {
			return pos, fmt.Errorf("scan query of event from (%s): %v", s[pos:posNext], err)
		}
		return posNext, nil
	case util.EventStmtExecute:
		// stmt-id
		if len(s) < pos+1 {
			return pos, fmt.Errorf("scan stmt-id of event from an empty string")
		}
		posNext = nextSep(s, pos)
		event.StmtID = string(s[pos:posNext])
		pos = posNext + 1
		// params
		event.Params, posNext, err = ScanStmtParams(s, pos, event.Params[:0])
		if err != nil {
			return pos, fmt.Errorf("scan params of event from (%s): %v", s[pos:posNext], err)
		}
		return posNext, nil
	case util.EventStmtPrepare:
		// stmt-id
		if len(s) < pos+1 {
			return pos, fmt.Errorf("scan stmt-id of event from an empty string")
		}
		posNext = nextSep(s, pos)
		event.StmtID = string(s[pos:posNext])
		pos = posNext + 1
		// query
		if len(s) < pos+1 {
			return pos, fmt.Errorf("scan query of event from an empty string")
		}
		posNext = nextSep(s, pos)
		event.Query, err = strconv.Unquote(s[pos:posNext])
		if err != nil {
			return pos, fmt.Errorf("scan query of event from (%s): %v", s[pos:posNext], err)
		}
		return posNext, nil
	case util.EventStmtClose:
		// stmt-id
		if len(s) < pos+1 {
			return pos, fmt.Errorf("scan stmt-id of event from an empty string")
		}
		posNext = nextSep(s, pos)
		event.StmtID = string(s[pos:posNext])
		return posNext, nil
	case util.EventHandshake:
		// db
		if len(s) < pos+1 {
			return pos, fmt.Errorf("scan db of event from an empty string")
		}
		posNext = nextSep(s, pos)
		event.DB, err = strconv.Unquote(s[pos:posNext])
		if err != nil {
			return pos, fmt.Errorf("scan db of event from (%s): %v", s[pos:posNext], err)
		}
		return posNext, nil
	case util.EventQuit:
		return posNext, nil
	default:
		return pos, fmt.Errorf("unknown event type: %v", event.Type)
	}
}

func ScanStmtParams(s string, pos int, params []interface{}) ([]interface{}, int, error) {
	if len(s) < pos+1 {
		return nil, pos, fmt.Errorf("scan params from an empty string")
	} else if s[pos] != typeLst {
		return nil, pos, fmt.Errorf("scan params from (%s)", s[pos:])
	}
	// s[pos] == '['
	pos += 1
	posNext := nextSep(s, pos)
	types := []byte(s[pos:posNext])

	for i, t := range types {
		pos = posNext + 1
		posNext = nextSep(s, pos)
		if pos == posNext {
			return nil, pos, fmt.Errorf("scan params[%d] from (%s)", i, s[pos:])
		}
		raw := s[pos:posNext]
		switch t {
		case typeNil:
			params = append(params, nil)
		case typeI64:
			val, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return nil, pos, fmt.Errorf("parse params[%d] from (%s) as i64: %v", i, raw, err)
			}
			params = append(params, val)
		case typeU64:
			val, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return nil, pos, fmt.Errorf("parse params[%d] from (%s) as u64: %v", i, raw, err)
			}
			params = append(params, val)
		case typeStr:
			val, err := strconv.Unquote(raw)
			if err != nil {
				return nil, pos, fmt.Errorf("parse params[%d] from (%s) as str: %v", i, raw, err)
			}
			params = append(params, val)
		case typeF32:
			val, err := strconv.ParseFloat(raw, 32)
			if err != nil {
				return nil, pos, fmt.Errorf("parse params[%d] from (%s) as f32: %v", i, raw, err)
			}
			params = append(params, float32(val))
		case typeF64:
			val, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, pos, fmt.Errorf("parse params[%d] from (%s) as f64: %v", i, raw, err)
			}
			params = append(params, val)
		case typeBin:
			str, err := strconv.Unquote(raw)
			if err != nil {
				return nil, pos, fmt.Errorf("parse params[%d] from (%s) as hex: %v", i, raw, err)
			}
			val, err := hex.DecodeString(str)
			if err != nil {
				return nil, pos, fmt.Errorf("parse params[%d] from (%s) as bin: %v", i, raw, err)
			}
			params = append(params, val)
		default:
			return nil, pos, fmt.Errorf("unsupported param type: %v", t)
		}
	}
	return params, posNext, nil
}

func nextSep(s string, pos int) int {
	size := len(s)
	if pos >= size {
		return size
	}
	off := strings.IndexByte(s[pos:], sep)
	if off == -1 {
		return size
	}
	return pos + off
}

func NewFactoryFromEventHandler(factory func(ConnID) MySQLEventHandler, opts FactoryOptions) *mysqlStreamFactory {
	f := defaultHandlerFactory
	if factory != nil {
		f = func(conn ConnID) MySQLPacketHandler {
			impl := factory(conn)
			if impl == nil {
				return RejectConn(conn)
			}
			return &eventHandler{
				fsm:  NewMySQLFSM(conn.Logger("mysql-stream")),
				conn: conn,
				impl: impl,
			}
		}
	}
	return &mysqlStreamFactory{new: f, opts: opts}
}

type MySQLEventHandler interface {
	OnEvent(event MySQLEvent)
	OnClose()
}

type eventHandler struct {
	fsm  *MySQLFSM
	conn ConnID
	impl MySQLEventHandler
}

func (h *eventHandler) Accept(ci gopacket.CaptureInfo, dir reassembly.TCPFlowDirection, tcp *layers.TCP) bool {
	return true
}

func (h *eventHandler) ParsePacket(pkt MySQLPacket) *MySQLEvent {
	h.fsm.Handle(pkt)
	if !h.fsm.Ready() || !h.fsm.Changed() {
		//h.fsm.log.Warn("packet is not ready")
		return nil
	}
	e := &MySQLEvent{
		Conn: pkt.Conn,
		Time: pkt.Time,
	}
	switch h.fsm.State() {
	case util.StateComQuery2:
		e.Type = util.EventQuery
		e.Query = h.fsm.Query()

	case util.StateComStmtExecute2:
		stmt := h.fsm.Stmt()
		e.Type = util.EventStmtExecute
		e.StmtID = strconv.FormatUint(uint64(stmt.ID), 10)
		e.Params = h.fsm.StmtParams()

	case util.StateComStmtPrepare1:
		stmt := h.fsm.Stmt()
		e.Type = util.EventStmtPrepare
		e.StmtID = strconv.FormatUint(uint64(stmt.ID), 10)
		e.Query = stmt.Query

	case util.StateComStmtClose:
		stmt := h.fsm.Stmt()
		e.Type = util.EventStmtClose
		e.StmtID = strconv.FormatUint(uint64(stmt.ID), 10)

	case util.StateHandshake1:
		e.Type = util.EventHandshake
		e.DB = h.fsm.Schema()
		e.Username = h.fsm.Username()

	case util.StateComQuit:
		e.Type = util.EventQuit
	default:
		return nil
	}
	return e
}

func (h *eventHandler) AsyncParsePacket() {
	h.fsm.log.Info("thread begin to run for parse packet " + h.conn.HashStr())
	for {
		pkt, ok := <-h.fsm.c
		if ok {
			e := h.ParsePacket(pkt)
			if e == nil {
				continue
			}
			stats.AddStatic("DealPacket", 1, false)
			e.Pr = h.fsm.pr
			h.fsm.pr = nil
			h.impl.OnEvent(*e)
		} else {
			h.fsm.wg.Done()
			h.fsm.log.Info("thread end to run for parse packet " + h.conn.HashStr())
			return
		}
	}
}

//deal  packet from pacp file
func (h *eventHandler) OnPacket(pkt MySQLPacket) {

	h.fsm.once.Do(func() {
		h.fsm.wg.Add(1)
		go h.AsyncParsePacket()
	})
	h.fsm.c <- pkt
	stats.AddStatic("ReadPacket", 1, false)
	stats.AddStatic("PacketChanLen", uint64(len(h.fsm.c)), true)
	/*
		if len(h.fsm.c) >90000 && len(h.fsm.c)% 1000 ==0 {
			h.fsm.log.Warn("packet Channel is nearly  full , " + fmt.Sprintf("%v-%v",len(h.fsm.c),100000))
		}
	*/
}

func (h *eventHandler) OnClose() {
	close(h.fsm.c)
	h.fsm.wg.Wait()
	h.impl.OnClose()
}
