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
	"time"

	"github.com/pingcap/go-ycsb/pkg/prop"
	"github.com/pingcap/go-ycsb/pkg/util"
	"github.com/pingcap/go-ycsb/pkg/ycsb"
	"github.com/spf13/cobra"
)

var cleanConfirm bool

func newCleanCommand() *cobra.Command {
	m := &cobra.Command{
		Use:   "clean db",
		Short: "Clean (delete) all keys in the database table",
		Long: `Clean (delete) all keys in the specified database table.
This is a destructive operation and cannot be undone.

Examples:
  go-ycsb clean tikv -p tikv.pd=127.0.0.1:2379 -p tikv.type=raw -p tikv.apiversion=V2 --table usertable --confirm
  go-ycsb clean tikv -p tikv.pd=127.0.0.1:2379 -p tikv.type=txn --table usertable --confirm

For TiKV raw mode, tikv.apiversion is required (V1 or V2).`,
		Args: cobra.MinimumNArgs(1),
		Run:  runCleanCommandFunc,
	}

	m.Flags().StringSliceVarP(&propertyFiles, "property_file", "P", nil, "Specify a property file")
	m.Flags().StringArrayVarP(&propertyValues, "prop", "p", nil, "Specify a property value with name=value")
	m.Flags().StringVar(&tableName, "table", "", "Use the table name instead of the default \""+prop.TableNameDefault+"\"")
	m.Flags().BoolVar(&cleanConfirm, "confirm", false, "Confirm the clean operation (required to prevent accidental deletion)")

	return m
}

func runCleanCommandFunc(cmd *cobra.Command, args []string) {
	dbName := args[0]
	initialGlobal(dbName, nil)

	// Validate tikv.apiversion for TiKV raw mode
	if dbName == "tikv" {
		tikvType := globalProps.GetString("tikv.type", "raw")
		if tikvType == "raw" {
			if _, ok := globalProps.Get("tikv.apiversion"); !ok {
				fmt.Println("ERROR: Missing required parameter '-p tikv.apiversion' for TiKV raw mode.")
				fmt.Println("Please specify the API version: -p tikv.apiversion=V1 or -p tikv.apiversion=V2")
				return
			}
		}
	}

	fmt.Println("***************** properties *****************")
	for key, value := range globalProps.Map() {
		fmt.Printf("\"%s\"=\"%s\"\n", key, value)
	}
	fmt.Println("**********************************************")

	// Safety check - require --confirm flag
	if !cleanConfirm {
		fmt.Println("ERROR: This is a destructive operation that will delete ALL keys in the table.")
		fmt.Println("Please add the --confirm flag to proceed:")
		fmt.Printf("  go-ycsb clean %s --confirm ...\n", dbName)
		return
	}

	// Check if database implements CleanDB interface
	cleanDB, ok := globalDB.(ycsb.CleanDB)
	if !ok {
		fmt.Printf("Database '%s' does not implement Clean operation\n", dbName)
		fmt.Println("Only the following databases support clean operation:")
		fmt.Println("  - TiKV (raw mode)")
		fmt.Println("  - TiKV (txn mode)")
		return
	}

	fmt.Printf("WARNING: You are about to delete ALL keys in table '%s'\n", tableName)
	fmt.Println("This operation cannot be undone!")
	fmt.Print("Type 'yes' to continue: ")

	var userInput string
	fmt.Scanln(&userInput)

	if userInput != "yes" {
		fmt.Println("Operation cancelled.")
		return
	}

	ctx := globalContext
	start := time.Now()

	fmt.Printf("Cleaning all keys in table '%s'...\n", tableName)

	err := cleanDB.Clean(ctx, tableName)
	if err != nil {
		util.Fatalf("Clean failed: %v", err)
	}

	elapsed := time.Since(start)
	fmt.Println("**********************************************")
	fmt.Printf("Clean completed successfully\n")
	fmt.Printf("Time elapsed: %s\n", elapsed)
	fmt.Println("**********************************************")
}
