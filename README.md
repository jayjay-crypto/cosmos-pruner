# Cosmos-Pruner (Axelar / Cosmos SDK 0.50)

Fork of [b-harvest/cosmos-pruner](https://github.com/b-harvest/cosmos-pruner), updated for chains running **Cosmos SDK v0.50** with **IAVL v1.2+** — tested against [Axelar v1.3.10](https://github.com/axelarnetwork/axelar-core/tree/v1.3.10).

Offline pruning tool for application state (IAVL stores) and CometBFT block/state data. Use when on-chain pruning (`pruning=everything`) is not enough to reclaim disk already consumed by historic data.

## Stack alignment (Axelar v1.3.10)

| Dependency | Version |
|------------|---------|
| cosmossdk.io/store | v1.1.1 |
| github.com/cosmos/iavl | v1.2.4 |
| github.com/cometbft/cometbft | v0.38.21 |
| github.com/cometbft/cometbft-db | v0.14.1 |
| github.com/cosmos/cosmos-db | v1.1.1 |

Store keys are discovered dynamically from the node database (includes Axelar modules: `wasm`, `evm`, `nexus`, `axelarnet`, etc.).

## Build

Requires **Go 1.23+**.

```bash
git clone https://github.com/jayjay-crypto/cosmos-pruner.git
cd cosmos-pruner
make build
```

On Windows (PowerShell):

```powershell
go build -tags pebbledb -o build/cosmos-pruner.exe main.go
```

## Usage (Axelar)

```bash
# Stop the node first (and vald/tofnd if validator)
systemctl stop axelard

# Backup
cp -a ~/.axelar/data ~/.axelar-data-backup-$(date +%F)

# Prune (default home: ~/.axelar, data: ~/.axelar/data)
./build/cosmos-pruner prune ~/.axelar/data \
  --backend=goleveldb \
  --blocks=100 \
  --versions=100 \
  --compact=true

# Restart
systemctl start axelard
```

Adjust the data path if your setup uses `~/.axelar/.core/data` instead.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--blocks` | 10 | CometBFT blocks to keep |
| `--versions` | 10 | Application state versions to keep (prefer round numbers: 100, 1000, …) |
| `--backend` | goleveldb | DB backend: `goleveldb` or `pebbledb` (must match `app.toml`) |
| `--cosmos-sdk` | true | Prune application state |
| `--tendermint` | true | Prune blockstore and state |
| `--tx_index` | true | Prune `tx_index` DB |
| `--compact` | true | Compact DBs after pruning |

## Disclaimer

- Always stop the node before pruning.
- Keep a full backup of `data/`.
- Test on testnet or a non-critical node first.
- Not officially supported by Axelar Network.

## Upstream

Based on [b-harvest/cosmos-pruner](https://github.com/b-harvest/cosmos-pruner) (Cosmos SDK 0.50).
