curl -X POST http://192.168.1.103:12345/api/ebpf/entry \
  -H "Content-Type: application/json" \
  -d '{
    "ifindex": 1344,
    "srcMac": "1a:e3:3c:5a:ec:5e",
    "throttleRateBps": 1000000,
    "delay": 1000,
    "lossRate": 500,
    "jitter": 10
  }'

curl -X DELETE http://192.168.1.103:12345/api/ebpf/entry \
  -H "Content-Type: application/json" \
  -d '{
    "ifindex": 1344,  
    "srcMac": "1a:e3:3c:5a:ec:5e" 
  }'

  curl -X POST http://localhost:8080/apis/emunet.emunet.io/v1/namespaces/default/emunets \
  -H "Content-Type: application/yaml" \
  --data-binary @config/samples/emunet_v1_emunet.yaml

  curl -X DELETE http://localhost:8080/apis/emunet.emunet.io/v1/namespaces/default/emunets/emunet-example