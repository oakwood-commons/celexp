// This is a standalone embedder-scenario module used to prove that an external
// application can consume github.com/oakwood-commons/celexp with no dependency on
// scafctl. It uses a local replace during development; once celexp is tagged, the
// replace can be dropped in favor of a pinned version.
module github.com/oakwood-commons/celexp/examples/embedder

go 1.26.6

require github.com/oakwood-commons/celexp v0.0.0

require (
	cel.dev/expr v0.25.2 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/google/cel-go v0.30.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/exp v0.0.0-20260410095643-746e56fc9e2f // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/oakwood-commons/celexp => ../..
