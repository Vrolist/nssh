module github.com/Vrolist/nssh

go 1.20

require (
	github.com/Vrolist/nssh/base_core v0.0.0
	github.com/Vrolist/nssh/base_tunnel v0.0.0
	github.com/Vrolist/nssh/platform v0.0.0-00010101000000-000000000000
	github.com/spf13/pflag v1.0.10
	golang.org/x/sys v0.30.0
)

require (
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.31.0 // indirect
	golang.org/x/crypto v0.33.0 // indirect
)

replace github.com/Vrolist/nssh/base_core => ./base_core

replace github.com/Vrolist/nssh/base_tunnel => ./base_tunnel

replace github.com/Vrolist/nssh/platform => ./platform
