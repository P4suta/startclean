# Startclean

Find and remove orphaned Windows Start Menu shortcuts with explicit review.

## Installation

```sh
git clone https://github.com/P4suta/startclean.git
cd startclean
mise trust mise.toml
mise install go
mise x go -- go build -o startclean.exe ./cmd/startclean
```

## Usage

```powershell
.\startclean.exe scan
```

## Documentation

[Read the documentation](docs/README.md).

## License

Licensed under either of the [Apache License, Version 2.0](LICENSE-APACHE) or the [Massachusetts Institute of Technology (MIT) license](LICENSE-MIT), at your option.
You license each contribution you submit under the same two licenses, without other terms, unless you state otherwise.
