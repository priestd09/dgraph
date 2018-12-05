// +build !oss

/*
 * Copyright 2018 Dgraph Labs, Inc. and Contributors
 *
 * Licensed under the Dgraph Community License (the "License"); you
 * may not use this file except in compliance with the License. You
 * may obtain a copy of the License at
 *
 *     https://github.com/dgraph-io/dgraph/blob/master/licenses/DCL.txt
 */

package backup

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"time"

	"github.com/dgraph-io/badger"
	"github.com/dgraph-io/badger/options"
	"github.com/dgraph-io/dgraph/ee/backup"
	"github.com/dgraph-io/dgraph/protos/pb"
	"github.com/dgraph-io/dgraph/x"
	"github.com/golang/glog"
)

const bufSize = 16 << 10

func runRestore() error {
	req := &backup.Request{
		Backup: &pb.BackupRequest{Source: opt.loc},
	}
	f, err := req.OpenLocation(opt.loc)
	if err != nil {
		return err
	}

	bo := badger.DefaultOptions
	bo.SyncWrites = false
	bo.TableLoadingMode = options.MemoryMap
	bo.ValueThreshold = 1 << 10
	bo.NumVersionsToKeep = math.MaxInt32
	bo.Dir = opt.pdir
	bo.ValueDir = opt.pdir
	db, err := badger.OpenManaged(bo)
	if err != nil {
		return err
	}
	defer db.Close()

	writer := x.NewTxnWriter(db)
	writer.BlindWrite = true

	var (
		kvs pb.KVS
		bb  bytes.Buffer
		sz  uint64
		cnt int
	)
	kvs.Kv = make([]*pb.KV, 0, 1000)

	br := bufio.NewReaderSize(f, bufSize)
	start := time.Now()
	for {
		err = binary.Read(br, binary.LittleEndian, &sz)
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		e := &pb.KV{}
		n, err := bb.ReadFrom(io.LimitReader(br, int64(sz)))
		if err != nil {
			return err
		}
		if n != int64(sz) {
			return x.Errorf("Restore failed read. Expected %d bytes but got %d instead.", sz, n)
		}
		if err = e.Unmarshal((&bb).Bytes()); err != nil {
			return err
		}
		bb.Reset()
		kvs.Kv = append(kvs.Kv, e)
		kvs.Done = false
		cnt++
		if cnt%1000 == 0 {
			if err := writer.Send(&kvs); err != nil {
				return err
			}
			kvs.Kv = kvs.Kv[:0]
			kvs.Done = true
			if cnt%100000 == 0 {
				glog.V(3).Infof("--- writing %d keys", cnt)
			}
		}
	}
	if !kvs.Done {
		if err := writer.Send(&kvs); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	glog.Infof("Loaded %d keys in %s\n", cnt, time.Since(start).Round(time.Second))

	return nil
}
