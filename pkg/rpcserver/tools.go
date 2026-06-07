package rpcserver

import (
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type toolsService struct {
	rpcv1.UnimplementedToolsServiceServer
	db *cortexdb.DB
}
