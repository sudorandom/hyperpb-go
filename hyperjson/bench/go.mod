// Module bench holds hyperjson's comparative benchmarks. It is a separate
// module so its extra dependencies (encoding/json/v2 experiment, etc.) do not
// become dependencies of hyperpb itself.
module buf.build/go/hyperpb/hyperjson/bench

go 1.26

require (
	buf.build/go/hyperpb v0.0.0
	github.com/go-json-experiment/json v0.0.0-20260820222146-c27c302e5fc3
	google.golang.org/protobuf v1.36.9
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/planetscale/vtprotobuf v0.6.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/testify v1.10.0 // indirect
	github.com/timandy/routine v1.1.5 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace buf.build/go/hyperpb => ../..
