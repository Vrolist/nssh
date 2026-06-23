module nssh/base_tunnel

go 1.20

require (
	golang.org/x/crypto v0.17.0
	nssh/base_core v0.0.0
)

require (
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/rs/zerolog v1.31.0 // indirect
	golang.org/x/sys v0.15.0 // indirect
)

replace nssh_client/base_core => ../base_core
