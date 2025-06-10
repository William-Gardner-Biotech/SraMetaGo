# SraMetaGo

### This is a Work in Progress repo with lots of room for improvement

##

NCBI's tool to query the Sequence Read Archive (SRA) is a bit slower than desired. This tool was designed to speed up metadata queries to SRA so you can spend more time analyzing the data and less time waiting.


This Go program queries the NCBI SRA (Sequence Read Archive) database for sequencing experiment metadata based on a user-defined search term and date range. It fetches experiment packages in batches, parses the XML metadata, and writes selected fields to a tab-separated value (TSV) file. **Currently it outputs only select fields needed for another project I'm working on, but in the future I would like to add support for all possible (over 200) SRA metadata fields.**

## Features

- Easy install
- Multi-threaded fetching and parsing of SRA experiment packages.
- Query customization with search term and date range.
- Retry logic with exponential backoff for robust network calls.
- Extracts detailed metadata including BioProject, BioSample, Submitter, Location, Collection Date, and more.

## Installation

1. Ensure [Go](https://golang.org/dl/) is installed (Go 1.18+ recommended).
2. Clone or download this repository.
3. Install dependencies:
   ```bash
   go mod tidy
   ```

## Usage

![Made with VHS](https://vhs.charm.sh/vhs-YjfR66MVkms0dImbGKkwT.gif)

### Build the Program

```bash
go build -o sra_fetcher main.go
```

### Run the Program

```bash
./sra_fetcher -term="sars-cov-2" -start="2024/01/01" -end="2024/12/31" -api-key="YOUR_NCBI_API_KEY"
```

#### Flags

- `-term`: **(string)** Search term to query in the SRA database (default: `sars-cov-2 wastewater`).
- `-start`: **(string)** Start of the publication date range in `yyyy/mm/dd` format (default: `2024/09/15`).
- `-end`: **(string)** End of the publication date range in `yyyy/mm/dd` format (default: `2030/12/31`).
- `-api-key`: **(string)** Optional NCBI API key to improve request throughput.

### Output

A file named `parsed_metadata.YY.MM.DD.HH.MM.tsv` will be created in the current directory. The file contains the following columns:

- `RunAccession`
- `BioProject`
- `BioSample`
- `Submitter`
- `CollectionDate`
- `Location`
- `Population`
- `TotalSpots`
- `ReleaseDate`
- `LoadDate`

## Concurrency

The program processes SRA IDs in batches of 100 with a maximum of 10 concurrent worker goroutines. Progress is monitored and visualized via my [DNA-style progress bar](https://github.com/William-Gardner-Biotech/polybar).

## Dependencies

- [`polybar`](https://github.com/William-Gardner-Biotech/polybar): DNA-style progress bar used for visual feedback during batch processing.
- Standard Go libraries: `net/http`, `encoding/xml`, `sync`, `math`, etc.

## Error Handling

- Implements exponential backoff retries (up to 7 (magic number) attempts per batch).
- Handles missing metadata gracefully with fallback extraction strategies.

## License

MIT License

## Author

[William Gardner](https://github.com/William-Gardner-Biotech)
