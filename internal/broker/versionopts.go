package broker

import "google.golang.org/grpc"

// versionServerOptions returns the gRPC server options of the version-compat
// unit. That unit fills this function with interceptors that reject calls on
// connections already refused by Hello; this unit returns nil. New appends the
// result before Deps.ServerOptions, so version rejection runs first.
//
// The interceptors must be added with grpc.ChainUnaryInterceptor and
// grpc.ChainStreamInterceptor. grpc.UnaryInterceptor and grpc.StreamInterceptor
// sit at the outermost layer of the chain regardless of their position in the
// option list, which would let the access interceptor in Deps.ServerOptions run
// first.
func versionServerOptions() []grpc.ServerOption { return nil }
