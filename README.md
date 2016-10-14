# go-multistream

[![](https://img.shields.io/badge/made%20by-Protocol%20Labs-blue.svg?style=flat-square)](http://ipn.io)
[![](https://img.shields.io/badge/project-multiformats-blue.svg?style=flat-square)](http://github.com/multiformats/multiformats)
[![](https://img.shields.io/badge/freenode-%23ipfs-blue.svg?style=flat-square)](http://webchat.freenode.net/?channels=%23ipfs)
[![Coverage Status](https://coveralls.io/repos/github/multiformats/go-multistream/badge.svg?branch=master)](https://coveralls.io/github/multiformats/go-multistream?branch=master)

> an implementation of the multistream protocol in go

This package implements a simple stream router for the multistream-select protocol.
The protocol is defined [here](https://github.com/multiformats/multistream-select).

## Table of Contents


- [Install](#install)
- [Usage](#usage)
- [Maintainers](#maintainers)
- [Contribute](#contribute)
- [License](#license)

## Install

```sh
go get github.com/multiformats/go-multistream
```

## Usage

```go
package main

import (
	"fmt"
	ms "github.com/multiformats/go-multistream"
	"io"
	"net"
)

func main() {
	mux := ms.NewMultistreamMuxer()
	mux.AddHandler("/cats", func(rwc io.ReadWriteCloser) error {
		fmt.Fprintln(rwc, "HELLO I LIKE CATS")
		return rwc.Close()
	})
	mux.AddHandler("/dogs", func(rwc io.ReadWriteCloser) error {
		fmt.Fprintln(rwc, "HELLO I LIKE DOGS")
		return rwc.Close()
	})

	list, err := net.Listen("tcp", ":8765")
	if err != nil {
		panic(err)
	}

	for {
		con, err := list.Accept()
		if err != nil {
			panic(err)
		}

		go mux.Handle(con)
	}
}
```

## Maintainers

Captain: [@whyrusleeping](https://github.com/whyrusleeping).

## Contribute

Contributions welcome. Please check out [the issues](https://github.com/multiformats/go-multistream/issues).

Check out our [contributing document](https://github.com/multiformats/multiformats/blob/master/contributing.md) for more information on how we work, and about contributing in general. Please be aware that all interactions related to multiformats are subject to the IPFS [Code of Conduct](https://github.com/ipfs/community/blob/master/code-of-conduct.md).

Small note: If editing the Readme, please conform to the [standard-readme](https://github.com/RichardLitt/standard-readme) specification.

## License

[MIT](LICENSE)
