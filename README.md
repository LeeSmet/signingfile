# Signingfile

A small command line utility tool to convert the output of [the threefold minting code](https://github.com/threefoldtech/minting_v3)
to stellar transactions. For every entry in the input file, an appropriate transaction is made, and written to the output file
encoded in XDR format. Records are separated by a newline character.

## Building

Building requires a recent version of the `go` compiler to be installed on the system. It is assumed that `git` is
used to get the code.

```bash
git clone https://github.com/LeeSmet/signingfile
cd signingfile
go build
```

## Running

Once a local copy is available, you can run the code as follows

```bash
./signingfile -payoutsfile <OUTPUT_FROM_MINTING> -outputfile <FILE_TO_SIGN>
```

The `-h` flag can be used to see the available options.

## License

This project is licensed under the MIT license, available in the LICENSE file in the root of this repository.
