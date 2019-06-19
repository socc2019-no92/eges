### sample genesis.json
```json
{
    "config": {
        "chainId": 930412,
        "homesteadBlock": 0,
        "eip155Block": 0,
        "eip158Block": 0,
        "thw": {
            "SGX": false,
            "bootstrap": [
                {
                    "account": "0e95127d0cea333d4ed6a0b304aebea8d1c83bc0",
                    "ip": "127.0.0.1",
                    "port": "10000"
                },
                {
                    "account": "88fb7ec6b584fa741ad1a090977fadb20d1223da",
                    "ip": "127.0.0.1",
                    "port": "10001"
                },
                {
                    "account": "461a841c629df9b22769e103bd076ed4d1a01ed1",
                    "ip": "127.0.0.1",
                    "port": "10002"
                }
            ],
            "backoff_time": 0,
            "reg_per_blk": 1000,
            "registration_timeout": 10,
            "validate_timeout": 500,
            "election_timeout": 100
        }
    },
    "coinbase": "0x0000000000000000000000000000000000000000",
    "difficulty": "0x40000",
    "extraData": "",
    "gasLimit": "0xffffffff",
    "nonce": "0x0000000000000042",
    "mixhash": "0x0000000000000000000000000000000000000000000000000000000000000000",
    "parentHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
    "timestamp": "0x00",
    "alloc": {}
}
```



### sample commands
normal nodes       
```shell
 /usr/bin/nohup ./build/bin/geth --datadir data/02 --unlock 461a841c629df9b22769e103bd076ed4d1a01ed1 --password ./pass  --verbosity 4 --networkid 930412 --ipcdisable --port 61902 --rpc --rpccorsdomain "*" --rpcport 8102 --bootnodes enode://108af4e14c1451c73255c00cbff83330924da9b4b24ce57684dc34c8c5ae27a6e15f4a77a3e5c9724c7b0ecaa1435a9a3474520486186d90764193f7f2cf3d6b@127.0.0.1:30301 --consensusPort "10002" --consensusIP 127.0.0.1  --syncmode "full" --maxpeers 100 --nCandidates 3 --nAcceptors 4 --blockTimeout 20 --txnPerBlock 1000 --txnSize 100 --totalNodes 3 --mine --breakdown  1>>data/logs/02 2>&1 &
```

nodes with Geec API (for application testing)
```shell
./build/bin/geth --datadir data/00 --unlock 0e95127d0cea333d4ed6a0b304aebea8d1c83bc0 --password ./pass  --verbosity 4 --networkid 930412 --ipcdisable --port 61900 --rpc --rpccorsdomain "*" --rpcport 8100 --bootnodes enode://108af4e14c1451c73255c00cbff83330924da9b4b24ce57684dc34c8c5ae27a6e15f4a77a3e5c9724c7b0ecaa1435a9a3474520486186d90764193f7f2cf3d6b@127.0.0.1:30301 --consensusPort "10000" --consensusIP 127.0.0.1  --syncmode "full" --maxpeers 100 --nCandidates 3 --nAcceptors 4 --blockTimeout 20 --txnPerBlock 1000 --txnSize 100 --totalNodes 3 --mine --breakdown  --geecTxnPort 3333 1>>data/logs/00 2>&1 &
```


### Make
1. Ensure the code path is correct:
```shell
ROOT=$GOPATH/src/github.com/ethereum/go-ethereum
```
2. Compile the dependent election lib:
```shell
cd $ROOT/consensus/trustedHW/election/lib
cmake .
make
```
3. Make
```shell
cd $ROOT
make all
```
4. Config and start
```shell
cp config-test.json xxx.json

vim start.py //change the config_file_name to xxx.json
```

### Config
Explanation of the fields in `config.json`

| Parameter | explanation  | type(unit) |
|-----------|-------|-------------|
|bootstrap_nodes|number of bootstrap nodes|`int`|
|normal_nodes| number of normal ndoes|`int`|
|code_path| `$ROOT`|`string`|
|max_peer | number of peers each node is connected|`int`|
|committee_ratio|  One committee member every `x` node (avg) |`int`|
|validator_ratio|  One validator every `x` node (avg) |`int`|
|term_len | number of blocks each committee is reponsible for |`int`|
|reg_per_blk | max number of registration request in each block |`int`|
|validate_threshold | the threshold of validate reply to determine 'no network partition' | `float (0.0-1.0)` |
|committee_timeout | the timeout time for a force committee change without new blocks | `int (s)` |
|registration_timeout | the timeout time for resending a registration reqeust | `int (s)` |
| validate_timeout | the timeout time for resending validation request | `int (ms)`|
| election_timeout | the timeout time for re-invoking a leader election | `int (ms)`|
| backoff_time | wait some time before broadcasting a mined block (debug only, set to 0 otherwise) | `int (ms)`
