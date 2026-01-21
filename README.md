# CNI安装
```
cd ./emu-cni/
LOG_PATH=/var/log/emu-cni.log make emu-cni
make install ##需要sudo权限
cd ../mac-cni/
REMOTE_IP=192.168.1.104 make mac-cni
make install ##需要sudo权限
```
# Node Agent运行(在节点机执行，需要sudo权限)
```
cd ../emunet-operator-node/
KUBECONFIG=./ssl/emunet-operator.kubeconfig make run .
```
