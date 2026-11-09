package main

// Version 由构建时注入：
//   go build -ldflags "-X main.Version=1.1.7"
// 没注入就是 dev。
var Version = "dev"
