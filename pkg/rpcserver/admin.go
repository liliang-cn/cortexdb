package rpcserver

import (
	"context"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

type adminService struct {
	rpcv1.UnimplementedAdminServiceServer
	db     *cortexdb.DB
	dbPath string
}

func (s *adminService) Health(context.Context, *rpcv1.HealthRequest) (*rpcv1.HealthResponse, error) {
	return &rpcv1.HealthResponse{Ok: true}, nil
}

func (s *adminService) Info(context.Context, *rpcv1.InfoRequest) (*rpcv1.InfoResponse, error) {
	return &rpcv1.InfoResponse{
		Version:     cortexdbroot.Version,
		DbPath:      s.dbPath,
		HasEmbedder: s.db.HasEmbedder(),
	}, nil
}
