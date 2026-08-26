# Cosmos-Pruner (Axelar / Cosmos SDK 0.53)

Fork of [b-harvest/cosmos-pruner](https://github.com/b-harvest/cosmos-pruner), updated for **Axelar v1.5.x** (Cosmos SDK **v0.53**, IAVL v1.2+).

Offline pruning for application state (IAVL) and CometBFT block/state data.

## Stack alignment (Axelar v1.5.3)

| Dependency | Version |
|------------|---------|
| cosmossdk.io/store | v1.1.2 |
| github.com/cosmos/iavl | v1.2.8 (skip missing version roots while pruning) |
| github.com/cometbft/cometbft | v0.38.25 |
| github.com/cometbft/cometbft-db | v0.14.1 |
| github.com/cosmos/cosmos-db | v1.1.3 |

### SDK 0.53 offline pruning

Application pruning opens each IAVL store via `MutableTree` with:

- `SyncOption(true)` — fsync after deletions
- `AsyncPruningOption(false)` — wait until prune finishes

Store keys are discovered dynamically from the node DB (Axelar modules: `wasm`, `evm`, `nexus`, `axelarnet`, etc.).

## Build

Requires **Go 1.23+** (1.24/1.25 recommended).

```bash
git clone https://github.com/jayjay-crypto/cosmos-pruner.git
cd cosmos-pruner
make build
# or:
go build -tags pebbledb -o build/cosmos-pruner main.go
```

## Usage (Axelar)

```bash
systemctl stop axelard
# validators: also stop vald / tofnd

cp -a ~/.axelar/data ~/.axelar-data-backup-$(date +%F)

./build/cosmos-pruner prune ~/.axelar/data \
  --backend=goleveldb \
  --blocks=100 \
  --versions=100 \
  --compact=true

systemctl start axelard
```

Adjust the path if you use `~/.axelar/.core/data`.

### Compact rewrite (reclaim disk)

`ForceCompact` often leaves large LevelDB files. Rewrite copies **live keys only** into a fresh DB:

```bash
# Needs free disk ≈ size of application.db while running
./build/cosmos-pruner compact ~/.axelar/data --backend=goleveldb
# rewrite is ON by default for the compact command

# Or after prune:
./build/cosmos-pruner prune ~/.axelar/data \
  --backend=goleveldb --blocks=2 --versions=2 \
  --compact=true --rewrite=true
```

Then check size: `du -sh ~/.axelar/data/application.db`

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--blocks` | 10 | CometBFT blocks to keep |
| `--versions` | 10 | App-state IAVL versions to keep |
| `--backend` | goleveldb | `goleveldb` or `pebbledb` |
| `--cosmos-sdk` | true | Prune / compact application state |
| `--tendermint` | true | Prune / compact blockstore and state |
| `--tx_index` | true | Prune / compact `tx_index` |
| `--compact` | true | Compact after prune |
| `--rewrite` | false on prune, **true on compact** | Rewrite DBs (copy live keys) instead of ForceCompact |

## Disclaimer

- Always stop the node before pruning.
- Keep a full backup of `data/`.
- Test on testnet or a non-critical node first.
- Not officially supported by Axelar Network.

## Upstream

Based on [b-harvest/cosmos-pruner](https://github.com/b-harvest/cosmos-pruner). Inspired by [akash-network/cosmprund](https://github.com/akash-network/cosmprund) sync-IAVL approach for SDK 0.53.
