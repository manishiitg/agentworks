module github.com/manishiitg/coding-agent-loop/mcp-gateway

go 1.26.0

require (
	github.com/manishiitg/coding-agent-loop/mcpoauth v0.0.0
	github.com/manishiitg/coding-agent-loop/workspace v0.0.0
	github.com/mark3labs/mcp-go v1.0.0
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2
	modernc.org/sqlite v1.49.1
)

replace github.com/manishiitg/coding-agent-loop/mcpoauth => ../agent_go/pkg/mcpoauth

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	modernc.org/libc v1.72.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace github.com/manishiitg/coding-agent-loop/workspace => ../workspace
