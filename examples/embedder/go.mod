// This is a standalone embedder-scenario module used to prove that an external
// application can consume github.com/oakwood-commons/celexp with no dependency on
// scafctl. The local replace directive exercises the current working tree.
module github.com/oakwood-commons/celexp/examples/embedder

go 1.27.1

require github.com/oakwood-commons/celexp v0.4.0

require (
	cel.dev/expr v0.26.0 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/google/cel-go v0.31.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	go.yaml.in/yaml/v3 v3.0.6 // indirect
	golang.org/x/exp v0.0.0-20261124000051-d6646c509b6d // indirect
	golang.org/x/text v0.44.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20261123191846-1e707ab9fbc2 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261123191846-1e707ab9fbc2 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/oakwood-commons/celexp => ../..
