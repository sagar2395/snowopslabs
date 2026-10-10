module github.com/sagar2395/snowopslabs

go 1.25.0

// The language floor above stays at 1.25; this pins the toolchain that BUILDS
// it. Without it `make vuln` passes in CI (which tracks the latest 1.26 patch)
// and fails on any developer machine whose Go has fallen behind a stdlib CVE.
toolchain go1.26.9

require (
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.1
	github.com/gorilla/websocket v1.5.3
	github.com/spf13/cobra v1.10.2
	golang.org/x/crypto v0.55.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.38.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/exp v0.0.0-20250620022241-b7579e27df2b // indirect
	golang.org/x/sys v0.47.0 // indirect
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15 // indirect
	modernc.org/libc v1.66.7 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
