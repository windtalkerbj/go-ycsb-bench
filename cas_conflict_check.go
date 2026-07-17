// cas_conflict_check.go — 验证 TiKV RawKV CAS 在不同 key 分布下的冲突率
//
// 用法:
//   go run cas_conflict_check.go [pdAddr] [dist] [numKeys] [numOps] [threads] [apiVer]
//   dist: uniform | zipfian
//   apiVer: v1 | v2 (默认 v2)
//
// 流程: 预写 numKeys 个 1KB value -> 多线程执行 Get+CompareAndSwap -> 统计冲突率
// 注意: atomic 模式由 SetAtomicForCAS(true) 开启；RawCAS 是否需要服务端 api-version=2 待实测
package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pingcap/kvproto/pkg/kvrpcpb"
	"github.com/tikv/client-go/v2/config"
	"github.com/tikv/client-go/v2/rawkv"

	"github.com/pingcap/go-ycsb/pkg/generator"
	"github.com/pingcap/go-ycsb/pkg/ycsb"
)

func main() {
	pdAddr := "127.0.0.1:2379"
	dist := "zipfian"
	numKeys := int64(100000)
	numOps := int64(200000)
	threads := int64(64)
	apiVer := "v2"

	if len(os.Args) > 1 {
		pdAddr = os.Args[1]
	}
	if len(os.Args) > 2 {
		dist = os.Args[2]
	}
	if len(os.Args) > 3 {
		numKeys, _ = strconv.ParseInt(os.Args[3], 10, 64)
	}
	if len(os.Args) > 4 {
		numOps, _ = strconv.ParseInt(os.Args[4], 10, 64)
	}
	if len(os.Args) > 5 {
		threads, _ = strconv.ParseInt(os.Args[5], 10, 64)
	}
	if len(os.Args) > 6 {
		apiVer = strings.ToLower(os.Args[6])
	}
	apiVersion := kvrpcpb.APIVersion_V2
	if apiVer == "v1" {
		apiVersion = kvrpcpb.APIVersion_V1
	}

	ctx := context.Background()
	config.UpdateGlobal(func(c *config.Config) {
		c.TiKVClient.GrpcConnectionCount = 16
		c.TiKVClient.MaxBatchSize = 128
	})

	db, err := rawkv.NewClientWithOpts(ctx, strings.Split(pdAddr, ","),
		rawkv.WithAPIVersion(apiVersion))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	db.SetAtomicForCAS(true)

	const valueSize = 100000 // 100KB，与 PERF 口径一致
	value := make([]byte, valueSize)
	vrand := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := range value {
		value[i] = byte(vrand.Intn(26) + 'a')
	}

	key := func(i int64) []byte {
		return []byte(fmt.Sprintf("cascheck:user%010d", i))
	}

	// 预写数据
	fmt.Printf("Preloading %d keys ...\n", numKeys)
	start := time.Now()
	for i := int64(0); i < numKeys; i++ {
		if err := db.Put(ctx, key(i), value); err != nil {
			panic(err)
		}
		if (i+1)%20000 == 0 {
			fmt.Printf("  loaded %d\n", i+1)
		}
	}
	fmt.Printf("Preload done in %v\n", time.Since(start))

	// 冲突率测试
	var successCnt, conflictCnt, errCnt, doneCnt int64
	var wg sync.WaitGroup
	start = time.Now()
	for t := int64(0); t < threads; t++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			tr := rand.New(rand.NewSource(seed))
			var keyGen ycsb.Generator
			if dist == "zipfian" {
				keyGen = generator.NewZipfianWithItems(numKeys, 0.99)
			} else {
				keyGen = generator.NewUniform(0, numKeys-1)
			}
			for {
				n := atomic.AddInt64(&doneCnt, 1)
				if n > numOps {
					return
				}
				k := key(keyGen.Next(tr))
				prev, err := db.Get(ctx, k)
				if err != nil {
					atomic.AddInt64(&errCnt, 1)
					continue
				}
				if prev == nil {
					if err := db.Put(ctx, k, value); err != nil {
						atomic.AddInt64(&errCnt, 1)
					}
					continue
				}
				newVal := make([]byte, valueSize)
				copy(newVal, prev)
				newVal[0] ^= 0xFF // 修改首字节即可，内容不重要
				_, ok, err := db.CompareAndSwap(ctx, k, prev, newVal)
				if err != nil {
					atomic.AddInt64(&errCnt, 1)
					continue
				}
				if ok {
					atomic.AddInt64(&successCnt, 1)
				} else {
					atomic.AddInt64(&conflictCnt, 1)
				}
			}
		}(time.Now().UnixNano() + t*7919)
	}
	wg.Wait()
	elapsed := time.Since(start)

	total := successCnt + conflictCnt
	fmt.Println("================ CAS 冲突率报告 ================")
	fmt.Printf("distribution : %s\n", dist)
	fmt.Printf("keys/ops/threads: %d / %d / %d\n", numKeys, numOps, threads)
	fmt.Printf("CAS success  : %d\n", successCnt)
	fmt.Printf("CAS conflict : %d\n", conflictCnt)
	fmt.Printf("errors       : %d\n", errCnt)
	if total > 0 {
		fmt.Printf("conflict rate: %.2f%%\n", float64(conflictCnt)*100/float64(total))
	}
	fmt.Printf("elapsed      : %v (%.0f ops/sec)\n", elapsed, float64(numOps)/elapsed.Seconds())
}
