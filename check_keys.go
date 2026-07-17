package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/pingcap/kvproto/pkg/kvrpcpb"
	"github.com/tikv/client-go/v2/config"
	"github.com/tikv/client-go/v2/rawkv"
)

func main() {
	ctx := context.Background()
	pdAddr := "127.0.0.1:2379"
	
	config.UpdateGlobal(func(c *config.Config) {
		c.TiKVClient.GrpcConnectionCount = 128
		c.TiKVClient.MaxBatchSize = 128
	})

	db, err := rawkv.NewClientWithOpts(ctx, strings.Split(pdAddr, ","),
		rawkv.WithAPIVersion(kvrpcpb.APIVersion_V2))
	if err != nil {
		panic(err)
	}
	defer db.Close()

	table := "usertable"
	startKey := []byte(fmt.Sprintf("%s:", table))
	endKey := append(startKey, 0xFF, 0xFF, 0xFF, 0xFF)

	fmt.Println("=== First 20 keys ===")
	keys, _, err := db.Scan(ctx, startKey, endKey, 20)
	if err != nil {
		panic(err)
	}
	for i, k := range keys {
		fmt.Printf("%d: %s\n", i, string(k))
	}

	fmt.Println("\n=== Last 20 keys ===")
	// Get total count first
	var count int64
	var batchStart = startKey
	for {
		batchKeys, _, err := db.Scan(ctx, batchStart, endKey, 10000)
		if err != nil {
			panic(err)
		}
		if len(batchKeys) == 0 {
			break
		}
		count += int64(len(batchKeys))
		if len(batchKeys) < 10000 {
			if len(batchKeys) >= 20 {
				fmt.Println("Last 20 keys:")
				for i := len(batchKeys) - 20; i < len(batchKeys); i++ {
					fmt.Printf("%d: %s\n", i, string(batchKeys[i]))
				}
			}
			break
		}
		batchStart = append(batchKeys[len(batchKeys)-1], 0x00)
	}
	fmt.Printf("\nTotal count: %d\n", count)
}
