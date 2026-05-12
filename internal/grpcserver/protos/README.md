# externalgrpc protos

`externalgrpc.proto`, `externalgrpc.pb.go`, and `externalgrpc_grpc.pb.go` are
vendored verbatim from
[`k8s.io/autoscaler/cluster-autoscaler/cloudprovider/externalgrpc/protos`](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler/cloudprovider/externalgrpc/protos).

We vendor the generated `*.pb.go` instead of `go get`-ing the upstream
module because that pulls in the full `k8s.io/autoscaler/cluster-autoscaler`
go.mod, which would force a v0.36 bump on `k8s.io/api`, `apimachinery`,
`apiserver`, `client-go`, and `controller-runtime` — incompatible with the
v0.34.x versions our existing reconcilers use.

The generated files have no `k8s.io/*` imports: they encode the externalgrpc
wire types directly as protobuf messages and depend only on
`google.golang.org/protobuf` + `google.golang.org/grpc`, both already in
our `go.mod` transitively.

## Regenerating

When the upstream proto changes, re-copy the three files from the sibling
`../cluster-autoscaler/cloudprovider/externalgrpc/protos/` checkout. Don't
edit the generated `.pb.go` files by hand — they carry `DO NOT EDIT`
headers and will be overwritten on the next regen.
