package rpcserver

import (
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type memoryService struct {
	rpcv1.UnimplementedMemoryServiceServer
	db *cortexdb.DB
}
