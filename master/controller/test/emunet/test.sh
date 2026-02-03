curl -X POST http://localhost:12345/api/ebpf/entry \
  -H "Content-Type: application/json" \
  -d '{
    "ifindex": 348,
    "srcMac": "00:00:00:99:0b:b1",
    "throttleRateBps": 1000000,
    "delay": 1000,
    "lossRate": 500,
    "jitter": 10
  }'

curl -X DELETE http://localhost:12345/api/ebpf/entry \
  -H "Content-Type: application/json" \
  -d '{
    "ifindex": 348,  
    "srcMac": "00:00:00:99:0b:b1" 
  }'

  curl -X POST http://localhost:8080/apis/emunet.emunet.io/v1/namespaces/default/emunets \
  -H "Content-Type: application/yaml" \
  --data-binary @config/samples/emunet_v1_emunet.yaml

  curl -X DELETE http://localhost:8080/apis/emunet.emunet.io/v1/namespaces/default/emunets/emunet-example


curl -X POST http://localhost:8082/api/v1/ebpf/entry/by-pods \
  -H "Content-Type: application/json" \
  -d '{
    "pod1": "emunet-example-group0-0",
    "pod2": "emunet-example-group0-10",
    "throttleRateBps": 1000000,
    "delay": 1000,
    "lossRate": 2500,
    "jitter": 10
  }'

curl -X DELETE http://localhost:8082/api/v1/ebpf/entry/by-pods \
  -H "Content-Type: application/json" \
  -d '{
    "pod1": "emunet-example-group0-0",
    "pod2": "emunet-example-group0-10"
  }'