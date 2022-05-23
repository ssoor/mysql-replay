# mysql-replay
read network packets from the production environment, parse them and persist
the production and simulation result sets in a file after playback from the
simulation environment

# 仿真工具
读取生产环境的网络包，解析网络包并在仿真环境执行后将生产环境结果集和仿真环境结果集持久化到文件中


# demo - online
```
./mysql-replay online replay --device=ens33 --srcPort=30696 -d"root:test34007@tcp(192.168.1.189:4002)/test"
```

# demo - dir
```
// generate pcap file
tcpdump -i ens37 -w pcaps/ens37.pcap

./mysql-replay dir replay --data-dir=pcaps --srcPort=30696 -d"root:test34007@tcp(192.168.1.189:4002)/test"
```

# demo - text
```
// generate pcap file
tcpdump -i ens37 -w pcaps/ens37.pcap

./mysql-replay text replay --srcPort=3306 -d"root:test34007@tcp(192.168.1.189:4002)/test" ./pcaps/mysql.pcap
```
