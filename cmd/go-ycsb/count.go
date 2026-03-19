// Copyright 2018 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pingcap/go-ycsb/pkg/prop"
	"github.com/pingcap/go-ycsb/pkg/util"
	"github.com/pingcap/go-ycsb/pkg/ycsb"
	"github.com/spf13/cobra"
)

var (
	countThreads int
	countBatch   int
)

func newCountCommand() *cobra.Command {
	m := &cobra.Command{
		Use:   "count db",
		Short: "Count keys in the database table",
		Long: `Count the total number of keys in the specified database table.
This command supports concurrent counting for better performance on large datasets.

Examples:
  go-ycsb count tikv -P workloads/workloada --threads 10
  go-ycsb count mysql -p mysql.host=127.0.0.1 -p mysql.db=test --table usertable`,
		Args: cobra.MinimumNArgs(1),
		Run:  runCountCommandFunc,
	}

	m.Flags().StringSliceVarP(&propertyFiles, "property_file", "P", nil, "Specify a property file")
	m.Flags().StringArrayVarP(&propertyValues, "prop", "p", nil, "Specify a property value with name=value")
	m.Flags().StringVar(&tableName, "table", "", "Use the table name instead of the default \""+prop.TableNameDefault+"\"")
	m.Flags().IntVar(&countThreads, "threads", 10, "Number of concurrent threads for counting")
	m.Flags().IntVar(&countBatch, "batch", 1000, "Batch size for scan operations (if applicable)")

	return m
}

func runCountCommandFunc(cmd *cobra.Command, args []string) {
	dbName := args[0]
	initialGlobal(dbName, nil)

	fmt.Println("***************** properties *****************")
	for key, value := range globalProps.Map() {
		fmt.Printf("\"%s\"=\"%s\"\n", key, value)
	}
	fmt.Println("**********************************************")

	// Check if database implements CountDB interface
	if countDB, ok := globalDB.(ycsb.CountDB); ok {
		fmt.Printf("Using native count implementation for %s\n", dbName)
		runNativeCount(countDB)
	} else {
		fmt.Printf("Using scan-based concurrent count for %s\n", dbName)
		runScanBasedCount()
	}
}

// runNativeCount uses the database's native Count implementation
func runNativeCount(countDB ycsb.CountDB) {
	ctx := globalContext
	start := time.Now()

	fmt.Printf("Counting keys in table '%s'...\n", tableName)

	count, err := countDB.Count(ctx, tableName)
	if err != nil {
		util.Fatalf("Count failed: %v", err)
	}

	elapsed := time.Since(start)
	fmt.Println("**********************************************")
	fmt.Printf("Total keys: %d\n", count)
	fmt.Printf("Time elapsed: %s\n", elapsed)
	fmt.Printf("Count rate: %.2f keys/sec\n", float64(count)/elapsed.Seconds())
	fmt.Println("**********************************************")
}

// runScanBasedCount uses concurrent scan operations to count keys
func runScanBasedCount() {
	ctx := globalContext
	start := time.Now()

	recordCount := globalProps.GetInt64(prop.RecordCount, prop.RecordCountDefault)
	keyPrefix := globalProps.GetString(prop.KeyPrefix, prop.KeyPrefixDefault)

	fmt.Printf("Counting keys in table '%s' using %d threads...\n", tableName, countThreads)
	fmt.Printf("Estimated record count: %d\n", recordCount)

	var totalCount int64
	var wg sync.WaitGroup

	// Calculate keys per thread
	keysPerThread := recordCount / int64(countThreads)
	if keysPerThread == 0 {
		keysPerThread = 1
	}

	// Progress reporting
	stopProgress := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				current := atomic.LoadInt64(&totalCount)
				fmt.Printf("\rProgress: %d keys counted...", current)
			case <-stopProgress:
				return
			}
		}
	}()

	// Launch concurrent workers
	for i := 0; i < countThreads; i++ {
		wg.Add(1)
		go func(threadID int) {
			defer wg.Done()

			// Initialize thread context
			threadCtx := globalDB.InitThread(ctx, threadID, countThreads)
			defer globalDB.CleanupThread(threadCtx)

			// Calculate range for this thread
			startIdx := int64(threadID) * keysPerThread
			endIdx := startIdx + keysPerThread
			if threadID == countThreads-1 {
				endIdx = recordCount // Last thread handles remaining keys
			}

			localCount := int64(0)

			// Scan in batches
			for idx := startIdx; idx < endIdx; idx += int64(countBatch) {
				batchSize := countBatch
				if idx+int64(batchSize) > endIdx {
					batchSize = int(endIdx - idx)
				}

				key := fmt.Sprintf("%s%d", keyPrefix, idx)
				rows, err := globalDB.Scan(threadCtx, tableName, key, batchSize, nil)
				if err != nil {
					// Key might not exist, continue
					continue
				}

				localCount += int64(len(rows))
			}

			atomic.AddInt64(&totalCount, localCount)
		}(i)
	}

	wg.Wait()
	close(stopProgress)

	elapsed := time.Since(start)
	fmt.Println("\n**********************************************")
	fmt.Printf("Total keys counted: %d\n", totalCount)
	fmt.Printf("Time elapsed: %s\n", elapsed)
	fmt.Printf("Count rate: %.2f keys/sec\n", float64(totalCount)/elapsed.Seconds())
	fmt.Printf("Threads used: %d\n", countThreads)
	fmt.Println("**********************************************")
}
