module nssh

go 1.22.0

toolchain go1.24.12

require (
	github.com/spf13/pflag v1.0.10
	golang.org/x/sys v0.30.0
	nssh/base_core v0.0.0
	nssh/base_tunnel v0.0.0
	nssh/platform v0.0.0-00010101000000-000000000000
)

require (
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.31.0 // indirect
	golang.org/x/crypto v0.33.0 // indirect
)

replace nssh/base_core => ./base_core

replace nssh/base_tunnel => ./base_tunnel

replace nssh/platform => ./platform
