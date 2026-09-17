// The interface's build tool, kept out of the server's module on purpose.
//
// The server depends on exactly one external package. A bundler in its go.mod
// would be a dependency of everybody who builds the server, and a dependency of
// the server in no other sense.
module picvert/tools/bundle

go 1.24

require github.com/evanw/esbuild v0.28.2

require golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8 // indirect
