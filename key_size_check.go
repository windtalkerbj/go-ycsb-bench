// key_size_check.go — 从 TiKV (raw, api-v1) 随机取一个 key，打印 value 字节数
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/pingcap/kvproto/pkg/kvrpcpb"
	"github.com/tikv/client-go/v2/rawkv"
)

func main() {
	ctx := context.Background()
	db, err := rawkv.NewClientWithOpts(ctx, strings.Split("127.0.0.1:2399", ","),
		rawkv.WithAPIVersion(kvrpcpb.APIVersion_V1))
	if err != nil {
		panic(err)
	}
	defer db.Close()

	startKey := []byte("usertable:")
	endKey := append(startKey, 0xFF, 0xFF, 0xFF, 0xFF)

	keys, values, err := db.Scan(ctx, startKey, endKey, 5)
	if err != nil {
		panic(err)
	}
	if len(keys) == 0 {
		fmt.Println("no keys found")
		return
	}
	// 取中间一个作为"随机"抽查样本
	i := len(keys) / 2
	for j, k := range keys {
		mark := ""
		if j == i {
			mark = "  <== 抽查"
		}
		fmt.Printf("key=%s  value_len=%d bytes%s\n", string(k), len(values[j]), mark)
	}
}
