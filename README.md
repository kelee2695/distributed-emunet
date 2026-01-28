# CNI安装
```
cd ./emu-cni/
LOG_PATH=/var/log/emu-cni.log make emu-cni
make install ##需要sudo权限
cd ../mac-cni/
REMOTE_IP=100.90.181.123 make mac-cni
make install ##需要sudo权限
```
# CNI配置文件导入
```
cd ../config
sudo cp ./00-EMU_MAC-conflist.conflist /etc/cni/net.d/
```
# Node Agent运行(在节点机执行，需要sudo权限)
```
cd ../emunet-operator-node/
KUBECONFIG=./ssl/emunet-operator.kubeconfig make run .
```
# Master controller运行(在master机器上执行)
```
cd ../emunet-operator-master/
KUBECONFIG=./ssl/emunet-operator.kubeconfig make run .
```
