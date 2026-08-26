package cmd

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	"github.com/cockroachdb/pebble"
	cometdb "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/state"
	"github.com/cometbft/cometbft/store"
	dbm "github.com/cosmos/cosmos-db"
	iavltree "github.com/cosmos/iavl"
	iavldb "github.com/cosmos/iavl/db"
	"github.com/jayjay-crypto/cosmos-pruner/internal/rootmulti"
	"github.com/spf13/cobra"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

// to figuring out the height to prune tx_index
var txIdxHeight int64 = 0

// load dbm
// load app store and prune
// if immutable tree is not deletable we should import and export current state

func pruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune [path_to_home]",
		Short: "prune data from the application store and block store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {

			//ctx := cmd.Context()
			//errs, _ := errgroup.WithContext(ctx)
			var err error
			if tendermint {
				if err = pruneTMData(args[0]); err != nil {
					fmt.Println(err.Error())
				}
			}

			if cosmosSdk {
				err = pruneAppState(args[0])
				if err != nil {
					fmt.Println(err.Error())
				}
			}

			if tx_idx {
				err = pruneTxIndex(args[0])
				if err != nil {
					fmt.Println(err.Error())
				}
			}

			return nil
		},
	}
	return cmd
}

func pruneTxIndex(home string) error {
	fmt.Println("pruning tx_index")
	txIdxDB, err := openCosmosDB("tx_index", home)
	if err != nil {
		return err
	}

	defer func() {
		errClose := txIdxDB.Close()
		if errClose != nil {
			fmt.Println(errClose.Error())
		}
	}()

	pruneHeight := txIdxHeight - int64(blocks) - 10
	if pruneHeight <= 0 {
		fmt.Printf("No need to prune (pruneHeight=%d)\n", pruneHeight)
		return nil
	}

	pruneBlockIndex(txIdxDB, pruneHeight)
	pruneTxIndexTxs(txIdxDB, pruneHeight)

	fmt.Println("finished pruning tx_index")

	if compact {
		fmt.Println("compacting tx_index")
		if err := compactCosmosDB(txIdxDB); err != nil {
			fmt.Println(err.Error())
		}
	}

	return nil
}

func pruneTxIndexTxs(db dbm.DB, pruneHeight int64) {
	itr, itrErr := db.Iterator(nil, nil)
	if itrErr != nil {
		panic(itrErr)
	}

	defer itr.Close()

	///////////////////////////////////////////////////
	// delete index by hash and index by height
	for ; itr.Valid(); itr.Next() {
		key := itr.Key()
		value := itr.Value()

		strKey := string(key)

		if strings.HasPrefix(strKey, "tx.height") { // index by height
			strs := strings.Split(strKey, "/")
			intHeight, _ := strconv.ParseInt(strs[2], 10, 64)

			if intHeight < pruneHeight {
				db.Delete(value)
				db.Delete(key)
			}
		} else {
			if len(value) == 32 { // maybe index tx by events
				strs := strings.Split(strKey, "/")
				if len(strs) == 4 { // index tx by events
					intHeight, _ := strconv.ParseInt(strs[2], 10, 64)
					if intHeight < pruneHeight {
						db.Delete(key)
					}
				}
			}
		}
	}
}

func pruneBlockIndex(db dbm.DB, pruneHeight int64) {
	itr, itrErr := db.Iterator(nil, nil)
	if itrErr != nil {
		panic(itrErr)
	}

	defer itr.Close()

	for ; itr.Valid(); itr.Next() {
		key := itr.Key()
		value := itr.Value()

		strKey := string(key)

		if strings.HasPrefix(strKey, "block.height") /* index block primary key*/ || strings.HasPrefix(strKey, "block_events") /* BeginBlock & EndBlock */ {
			intHeight := int64FromBytes(value)
			//fmt.Printf("intHeight: %d\n", intHeight)

			if intHeight < pruneHeight {
				db.Delete(key)
			}
		}
	}
}

func pruneAppState(home string) error {
	appDB, errDB := openCosmosDB("application", home)
	if errDB != nil {
		return errDB
	}
	defer appDB.Close()

	fmt.Println("pruning application state (IAVL sync mode for SDK 0.53+)")

	keys := getStoreKeys(appDB)
	latestVer := rootmulti.GetLatestVersion(appDB)
	if latestVer <= 0 {
		return fmt.Errorf("no valid latest application version found")
	}

	if txIdxHeight <= 0 {
		txIdxHeight = latestVer
		fmt.Printf("[pruneAppState] set txIdxHeight=%d\n", txIdxHeight)
	}

	keepRecent := int64(versions)
	if keepRecent <= 0 {
		keepRecent = 10
	}

	fmt.Printf("[pruneAppState] latest=%d keep=%d stores=%d\n", latestVer, keepRecent, len(keys))

	successCount, errorCount := 0, 0
	for _, name := range keys {
		fmt.Printf("[pruneAppState] store %s\n", name)
		if err := pruneIAVLTreeDirect(appDB, name, keepRecent); err != nil {
			fmt.Printf("[pruneAppState] store %s: %v\n", name, err)
			errorCount++
			continue
		}
		successCount++
	}
	fmt.Printf("[pruneAppState] stores done: ok=%d errors=%d\n", successCount, errorCount)

	if err := pruneCommitInfoMetadata(appDB, latestVer, keepRecent); err != nil {
		fmt.Printf("[pruneAppState] commit-info cleanup: %v\n", err)
	}

	if compact {
		fmt.Println("compacting application state")
		if err := compactCosmosDB(appDB); err != nil {
			fmt.Println(err.Error())
		}
	}

	fmt.Println("[pruneAppState] finished pruning application state")
	return nil
}

// pruneIAVLTreeDirect opens each store via MutableTree with Sync=true and AsyncPruning=false.
// This is required for reliable offline pruning on Cosmos SDK v0.53+ / Axelar v1.5+.
func pruneIAVLTreeDirect(appDB dbm.DB, storeName string, keepRecent int64) error {
	const incrementalBatch = 500

	prefix := fmt.Sprintf("s/k:%s/", storeName)
	prefixed := dbm.NewPrefixDB(appDB, []byte(prefix))
	tree := iavltree.NewMutableTree(
		iavldb.NewWrapper(prefixed),
		1000000,
		false,
		log.NewNopLogger(),
		iavltree.SyncOption(true),
		iavltree.AsyncPruningOption(false),
	)

	if _, err := tree.Load(); err != nil {
		return fmt.Errorf("load tree: %w", err)
	}

	vers := tree.AvailableVersions()
	if len(vers) == 0 {
		fmt.Printf("[pruneAppState] store %s: no versions\n", storeName)
		return nil
	}
	if int64(len(vers)) <= keepRecent {
		fmt.Printf("[pruneAppState] store %s: no prune needed (%d versions)\n", storeName, len(vers))
		return nil
	}

	sort.Ints(vers)
	target := int64(vers[len(vers)-int(keepRecent)-1])
	fmt.Printf("[pruneAppState] store %s: pruning up to %d (keep %d of %d, latest=%d)\n",
		storeName, target, keepRecent, len(vers), vers[len(vers)-1])

	if err := tree.DeleteVersionsTo(target); err == nil {
		after := tree.AvailableVersions()
		fmt.Printf("[pruneAppState] store %s: pruned OK (%d remaining)\n", storeName, len(after))
		return nil
	} else if !strings.Contains(err.Error(), "does not exist") {
		return err
	} else {
		fmt.Printf("[pruneAppState] store %s: batch prune failed (%v), pruning incrementally\n", storeName, err)
	}

	for int64(len(vers)) > keepRecent {
		endIdx := len(vers) - int(keepRecent) - 1
		if endIdx <= 0 {
			break
		}
		step := incrementalBatch
		if step > endIdx {
			step = endIdx
		}
		pruneTo := int64(vers[step-1])
		if err := tree.DeleteVersionsTo(pruneTo); err != nil {
			if strings.Contains(err.Error(), "does not exist") {
				vers = vers[1:]
				continue
			}
			return fmt.Errorf("incremental prune at %d: %w", pruneTo, err)
		}
		vers = tree.AvailableVersions()
		sort.Ints(vers)
	}
	fmt.Printf("[pruneAppState] store %s: done (%d versions remaining)\n", storeName, len(vers))
	return nil
}

func pruneCommitInfoMetadata(appDB dbm.DB, latestVer, keepRecent int64) error {
	target := latestVer - keepRecent
	if target <= 0 {
		return nil
	}

	fmt.Printf("[pruneAppState] cleaning commit-info metadata below %d\n", target)

	iter, err := appDB.Iterator([]byte("s/"), nil)
	if err != nil {
		return err
	}
	defer iter.Close()

	var toDelete [][]byte
	for ; iter.Valid(); iter.Next() {
		key := string(iter.Key())
		if !strings.HasPrefix(key, "s/") {
			break
		}
		if strings.HasPrefix(key, "s/k:") || key == "s/latest" || key == "s/pruneheights" {
			continue
		}
		var ver int64
		if _, err := fmt.Sscanf(key[2:], "%d", &ver); err != nil {
			continue
		}
		if ver < target {
			toDelete = append(toDelete, append([]byte(nil), iter.Key()...))
		}
	}

	if len(toDelete) == 0 {
		fmt.Println("[pruneAppState] no old commit-info entries")
		return nil
	}

	batch := appDB.NewBatch()
	defer batch.Close()
	count := 0
	for _, key := range toDelete {
		if err := batch.Delete(key); err != nil {
			return err
		}
		count++
		if count%10000 == 0 {
			if err := batch.Write(); err != nil {
				return err
			}
			batch.Close()
			batch = appDB.NewBatch()
		}
	}
	if err := batch.Write(); err != nil {
		return err
	}
	fmt.Printf("[pruneAppState] deleted %d commit-info entries\n", len(toDelete))
	return nil
}

// pruneTMData prunes the tendermint blocks and state based on the amount of blocks to keep
func pruneTMData(home string) error {
	blockStoreDB, errDBBlock := openCometBFTDB("blockstore", home)
	if errDBBlock != nil {
		return errDBBlock
	}

	blockStore := store.NewBlockStore(blockStoreDB)
	defer blockStore.Close()

	// Get StateStore
	stateDB, errDBBState := openCometBFTDB("state", home)
	if errDBBState != nil {
		return errDBBState
	}

	stateStore := state.NewStore(stateDB, state.StoreOptions{})
	defer stateStore.Close()

	base := blockStore.Base()
	height := blockStore.Height()

	pruneHeight := height - int64(blocks)
	fmt.Printf("[pruneTMData] base=%d height=%d pruneHeight=%d\n", base, height, pruneHeight)
	if pruneHeight <= base {
		fmt.Printf("[pruneTMData] No need to prune blocks (base %d >= target %d)\n", base, pruneHeight)
		return nil
	}
	if pruneHeight <= 0 {
		fmt.Println("[pruneTMData] No need to prune")
		return nil
	}

	if txIdxHeight <= 0 {
		txIdxHeight = blockStore.Height()
		fmt.Printf("[pruneTMData] set txIdxHeight=%d\n", txIdxHeight)
	}

	fmt.Println("pruning block/state store")
	state, err := stateStore.Load()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	var (
		prunedBlocksCount uint64
		endHeight         int64 = base
	)

	// prune block store
	// prune one by one instead of range to avoid `panic: pebble: batch too large: >= 4.0 G` issue
	// (see https://github.com/notional-labs/cosmprund/issues/11)
	for pruneStateFrom := base; pruneStateFrom < pruneHeight; pruneStateFrom += rootmulti.PRUNE_BATCH_SIZE {
		batchEnd := pruneStateFrom + rootmulti.PRUNE_BATCH_SIZE
		if batchEnd > pruneHeight {
			batchEnd = pruneHeight
		}
		if batchEnd <= base {
			continue
		}

		prunedBlocks, evidenceRetainBlocks, err := blockStore.PruneBlocks(batchEnd, state)
		if err != nil {
			fmt.Printf("[pruneTMData] PruneBlocks(%d) error: %s\n", batchEnd, err)
			continue
		}
		prunedBlocksCount += prunedBlocks

		endHeight += rootmulti.PRUNE_BATCH_SIZE
		if endHeight >= pruneHeight-1 {
			endHeight = pruneHeight - 1
		}

		_, err = stateStore.LoadConsensusParams(endHeight)
		if err != nil {
			continue
		}
		_, err = stateStore.LoadValidators(endHeight)
		if err != nil {
			continue
		}
		_, err = stateStore.LoadFinalizeBlockResponse(endHeight)
		if err != nil {
			continue
		}
		_, err = stateStore.LoadLastFinalizeBlockResponse(endHeight)
		if err != nil {
			continue
		}

		err = stateStore.PruneStates(pruneStateFrom, endHeight, evidenceRetainBlocks)
		if err != nil {
			fmt.Printf("failed to prune state store: %s", err)
		}
	}

	fmt.Printf("Pruned blocks count: %d\n", prunedBlocksCount)

	if compact {
		fmt.Println("compacting block store")
		if err := compactCometBFTDB(blockStoreDB); err != nil {
			fmt.Println(err.Error())
		}
	}

	if compact {
		fmt.Println("compacting state store")
		if err := compactCometBFTDB(stateDB); err != nil {
			fmt.Println(err.Error())
		}
	}

	return nil
}

// Utils
func openCosmosDB(dbname string, home string) (dbm.DB, error) {
	dbType := dbm.BackendType(backend)
	dbDir := rootify(dataDir, home)

	var db1 dbm.DB

	if dbType == dbm.GoLevelDBBackend {
		o := opt.Options{
			DisableSeeksCompaction: true,
		}

		lvlDB, err := dbm.NewGoLevelDBWithOpts(dbname, dbDir, &o)
		if err != nil {
			return nil, err
		}

		db1 = lvlDB
	} else if dbType == dbm.PebbleDBBackend {
		ppDB, err := dbm.NewPebbleDB(dbname, dbDir, dbm.OptionsMap{})
		if err != nil {
			return nil, err
		}

		db1 = ppDB
	} else {
		var err error
		db1, err = dbm.NewDB(dbname, dbType, dbDir)
		if err != nil {
			return nil, err
		}
	}

	return db1, nil
}

// Utils
func openCometBFTDB(dbname string, home string) (cometdb.DB, error) {
	dbType := cometdb.BackendType(backend)
	dbDir := rootify(dataDir, home)

	var db1 cometdb.DB

	if dbType == cometdb.GoLevelDBBackend {
		o := opt.Options{
			DisableSeeksCompaction: true,
		}

		lvlDB, err := cometdb.NewGoLevelDBWithOpts(dbname, dbDir, &o)
		if err != nil {
			return nil, err
		}

		db1 = lvlDB
	} else if dbType == cometdb.PebbleDBBackend {
		opts := &pebble.Options{
			//DisableAutomaticCompactions: true, // freeze when pruning!
		}
		opts.EnsureDefaults()

		ppDB, err := cometdb.NewPebbleDBWithOpts(dbname, dbDir, opts)
		if err != nil {
			return nil, err
		}

		db1 = ppDB
	} else {
		var err error
		db1, err = cometdb.NewDB(dbname, dbType, dbDir)
		if err != nil {
			return nil, err
		}
	}

	return db1, nil
}

func compactCosmosDB(vdb dbm.DB) error {
	dbType := dbm.BackendType(backend)

	if dbType == dbm.GoLevelDBBackend {
		vdbLevel := vdb.(*dbm.GoLevelDB)

		if err := vdbLevel.ForceCompact(nil, nil); err != nil {
			return err
		}
	} else if dbType == dbm.PebbleDBBackend {
		vdbPebble := vdb.(*dbm.PebbleDB).DB()

		iter, _ := vdbPebble.NewIter(nil)
		//defer iter.Close()

		var start, end []byte

		if iter.First() {
			start = cp(iter.Key())
		}

		if iter.Last() {
			end = cp(iter.Key())
		}

		// close iter before compacting
		iter.Close()

		err := vdbPebble.Compact(start, end, false)
		if err != nil {
			return err
		}
	}

	return nil
}

func compactCometBFTDB(vdb cometdb.DB) error {
	dbType := cometdb.BackendType(backend)

	if dbType == cometdb.GoLevelDBBackend {
		vdbLevel := vdb.(*cometdb.GoLevelDB)

		if err := vdbLevel.Compact(nil, nil); err != nil {
			return err
		}
	} else if dbType == cometdb.PebbleDBBackend {
		vdbPebble := vdb.(*cometdb.PebbleDB).DB()

		iter, _ := vdbPebble.NewIter(nil)
		//defer iter.Close()

		var start, end []byte

		if iter.First() {
			start = cp(iter.Key())
		}

		if iter.Last() {
			end = cp(iter.Key())
		}

		// close iter before compacting
		iter.Close()

		err := vdbPebble.Compact(start, end, false)
		if err != nil {
			return err
		}
	}

	return nil
}

func getStoreKeys(db dbm.DB) (storeKeys []string) {
	latestVer := rootmulti.GetLatestVersion(db)
	latestCommitInfo, err := getCommitInfo(db, latestVer)
	if err != nil {
		panic(err)
	}

	for _, storeInfo := range latestCommitInfo.StoreInfos {
		storeKeys = append(storeKeys, storeInfo.Name)
	}
	return
}

func getCommitInfo(db dbm.DB, ver int64) (*storetypes.CommitInfo, error) {
	const commitInfoKeyFmt = "s/%d" // s/<version>
	cInfoKey := fmt.Sprintf(commitInfoKeyFmt, ver)

	bz, err := db.Get([]byte(cInfoKey))
	if err != nil {
		return nil, fmt.Errorf("failed to get commit info: %s", err)
	} else if bz == nil {
		return nil, fmt.Errorf("no commit info found")
	}

	cInfo := &storetypes.CommitInfo{}
	if err = cInfo.Unmarshal(bz); err != nil {
		return nil, fmt.Errorf("failed unmarshal commit info: %s", err)
	}

	return cInfo, nil
}

func cp(bz []byte) (ret []byte) {
	ret = make([]byte, len(bz))
	copy(ret, bz)
	return ret
}

func rootify(path, root string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func int64FromBytes(bz []byte) int64 {
	v, _ := binary.Varint(bz)
	return v
}
