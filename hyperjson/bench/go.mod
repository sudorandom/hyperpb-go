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

require github.com/timandy/routine v1.1.5 // indirect

replace buf.build/go/hyperpb => ../..
