package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	cometdb "github.com/cometbft/cometbft-db"
	dbm "github.com/cosmos/cosmos-db"
)

const rewriteBatchSize = 10_000

// rewriteCosmosDB copies all live keys from name.db into a fresh DB, then swaps it in place.
// Requires roughly as much free disk as the live data size until the .bak is removed.
func rewriteCosmosDB(name, home string) error {
	dbDir := rootify(dataDir, home)
	srcPath := filepath.Join(dbDir, name+".db")
	tmpName := name + "_rewrite"
	tmpPath := filepath.Join(dbDir, tmpName+".db")
	bakPath := filepath.Join(dbDir, name+".db.bak")

	if _, err := os.Stat(srcPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("[rewrite] %s: skip (not found)\n", srcPath)
			return nil
		}
		return err
	}

	_ = os.RemoveAll(tmpPath)

	fmt.Printf("[rewrite] %s → fresh DB (streaming keys)...\n", srcPath)

	src, err := openCosmosDB(name, home)
	if err != nil {
		return fmt.Errorf("open source %s: %w", name, err)
	}

	dst, err := openCosmosDB(tmpName, home)
	if err != nil {
		_ = src.Close()
		return fmt.Errorf("open temp %s: %w", tmpName, err)
	}

	count, err := copyCosmosDB(src, dst)
	closeErr1 := src.Close()
	closeErr2 := dst.Close()
	if err != nil {
		_ = os.RemoveAll(tmpPath)
		return err
	}
	if closeErr1 != nil {
		return closeErr1
	}
	if closeErr2 != nil {
		return closeErr2
	}

	fmt.Printf("[rewrite] %s: copied %d keys, swapping...\n", name, count)

	_ = os.RemoveAll(bakPath)
	if err := os.Rename(srcPath, bakPath); err != nil {
		_ = os.RemoveAll(tmpPath)
		return fmt.Errorf("rename %s → bak: %w", srcPath, err)
	}
	if err := os.Rename(tmpPath, srcPath); err != nil {
		_ = os.Rename(bakPath, srcPath) // best-effort rollback
		return fmt.Errorf("rename rewrite → %s: %w", srcPath, err)
	}
	if err := os.RemoveAll(bakPath); err != nil {
		fmt.Printf("[rewrite] warning: could not remove %s: %v\n", bakPath, err)
	}

	fmt.Printf("[rewrite] %s: done\n", name)
	return nil
}

func copyCosmosDB(src, dst dbm.DB) (int64, error) {
	iter, err := src.Iterator(nil, nil)
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	batch := dst.NewBatch()
	defer batch.Close()

	var count int64
	pending := 0
	flush := func() error {
		if pending == 0 {
			return nil
		}
		if err := batch.Write(); err != nil {
			return err
		}
		batch.Close()
		batch = dst.NewBatch()
		pending = 0
		return nil
	}

	for ; iter.Valid(); iter.Next() {
		if err := batch.Set(cp(iter.Key()), cp(iter.Value())); err != nil {
			return count, err
		}
		count++
		pending++
		if pending >= rewriteBatchSize {
			if err := flush(); err != nil {
				return count, err
			}
			if count%1_000_000 == 0 {
				fmt.Printf("[rewrite] ... %d keys\n", count)
			}
		}
	}
	if err := flush(); err != nil {
		return count, err
	}
	return count, nil
}

// rewriteCometBFTDB is the CometBFT-db equivalent of rewriteCosmosDB.
func rewriteCometBFTDB(name, home string) error {
	dbDir := rootify(dataDir, home)
	srcPath := filepath.Join(dbDir, name+".db")
	tmpName := name + "_rewrite"
	tmpPath := filepath.Join(dbDir, tmpName+".db")
	bakPath := filepath.Join(dbDir, name+".db.bak")

	if _, err := os.Stat(srcPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("[rewrite] %s: skip (not found)\n", srcPath)
			return nil
		}
		return err
	}

	_ = os.RemoveAll(tmpPath)

	fmt.Printf("[rewrite] %s → fresh DB (streaming keys)...\n", srcPath)

	src, err := openCometBFTDB(name, home)
	if err != nil {
		return fmt.Errorf("open source %s: %w", name, err)
	}

	dst, err := openCometBFTDB(tmpName, home)
	if err != nil {
		_ = src.Close()
		return fmt.Errorf("open temp %s: %w", tmpName, err)
	}

	count, err := copyCometBFTDB(src, dst)
	closeErr1 := src.Close()
	closeErr2 := dst.Close()
	if err != nil {
		_ = os.RemoveAll(tmpPath)
		return err
	}
	if closeErr1 != nil {
		return closeErr1
	}
	if closeErr2 != nil {
		return closeErr2
	}

	fmt.Printf("[rewrite] %s: copied %d keys, swapping...\n", name, count)

	_ = os.RemoveAll(bakPath)
	if err := os.Rename(srcPath, bakPath); err != nil {
		_ = os.RemoveAll(tmpPath)
		return fmt.Errorf("rename %s → bak: %w", srcPath, err)
	}
	if err := os.Rename(tmpPath, srcPath); err != nil {
		_ = os.Rename(bakPath, srcPath)
		return fmt.Errorf("rename rewrite → %s: %w", srcPath, err)
	}
	if err := os.RemoveAll(bakPath); err != nil {
		fmt.Printf("[rewrite] warning: could not remove %s: %v\n", bakPath, err)
	}

	fmt.Printf("[rewrite] %s: done\n", name)
	return nil
}

func copyCometBFTDB(src, dst cometdb.DB) (int64, error) {
	iter, err := src.Iterator(nil, nil)
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	batch := dst.NewBatch()
	defer batch.Close()

	var count int64
	pending := 0
	flush := func() error {
		if pending == 0 {
			return nil
		}
		if err := batch.Write(); err != nil {
			return err
		}
		batch.Close()
		batch = dst.NewBatch()
		pending = 0
		return nil
	}

	for ; iter.Valid(); iter.Next() {
		if err := batch.Set(cp(iter.Key()), cp(iter.Value())); err != nil {
			return count, err
		}
		count++
		pending++
		if pending >= rewriteBatchSize {
			if err := flush(); err != nil {
				return count, err
			}
			if count%1_000_000 == 0 {
				fmt.Printf("[rewrite] ... %d keys\n", count)
			}
		}
	}
	if err := flush(); err != nil {
		return count, err
	}
	return count, nil
}

func rewriteOrForceCompactCosmos(name, home string) error {
	if rewrite {
		return rewriteCosmosDB(name, home)
	}
	db, err := openCosmosDB(name, home)
	if err != nil {
		return err
	}
	defer db.Close()
	fmt.Printf("[compact] ForceCompact %s...\n", name)
	return compactCosmosDB(db)
}

func rewriteOrForceCompactComet(name, home string) error {
	if rewrite {
		return rewriteCometBFTDB(name, home)
	}
	db, err := openCometBFTDB(name, home)
	if err != nil {
		return err
	}
	defer db.Close()
	fmt.Printf("[compact] Compact %s...\n", name)
	return compactCometBFTDB(db)
}
