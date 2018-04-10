# zbft

Byzantine fault tolerant consensus algorithm for [blockchain](https://github.com/hexablock/blockchain)

- Prepared Tx's are collected by each node
- Node proposes a new block
- Each participant signs the block
- Each participant persists after receiving enough signatures
- Each participant commits after receiving enough persists
- Each participant executes each Tx in the block
