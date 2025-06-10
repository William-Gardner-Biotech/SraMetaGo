// main.go
package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	// Import the DNA‐style progress bar package
	"github.com/William-Gardner-Biotech/polybar/polybar"
)

const batchSize = 100
const maxWorkers = 10

// ESearchResult represents the root XML structure returned by NCBI's ESearch API
type ESearchResult struct {
	XMLName  xml.Name `xml:"eSearchResult"`
	Count    int      `xml:"Count"`
	RetMax   int      `xml:"RetMax"`
	RetStart int      `xml:"RetStart"`
	QueryKey string   `xml:"QueryKey"`
	WebEnv   string   `xml:"WebEnv"`
	IdList   IdList   `xml:"IdList"`
}

// IdList contains the list of IDs returned by the search
type IdList struct {
	Ids []string `xml:"Id"`
}

// ExperimentPackageSet is the root element of the SRA metadata XML
type ExperimentPackageSet struct {
	XMLName  xml.Name            `xml:"EXPERIMENT_PACKAGE_SET"`
	Packages []ExperimentPackage `xml:"EXPERIMENT_PACKAGE"`
}

// ExperimentPackage represents a single experiment with all its components
type ExperimentPackage struct {
	Experiment   Experiment    `xml:"EXPERIMENT"`
	Sample       Sample        `xml:"SAMPLE"`
	RunSet       RunSet        `xml:"RUN_SET"`
	Platform     Platform      `xml:"PLATFORM"`
	Organization Organization  `xml:"Organization,omitempty"`
	ReleaseDate  string        `xml:"ReleaseDate,omitempty"`
	LoadDate     string        `xml:"LoadDate,omitempty"`
}

// externalID is a helper struct that captures any <EXTERNAL_ID> node,
// including its namespace attribute and inner text.
type externalID struct {
	Namespace string `xml:"namespace,attr"`
	Value     string `xml:",chardata"`
}

// studyRef captures all <EXTERNAL_ID> elements under STUDY_REF>IDENTIFIERS,
// so we can pick out the one whose namespace is "BioProject".
type studyRef struct {
	ExternalIDs []externalID `xml:"IDENTIFIERS>EXTERNAL_ID"`
}

// Experiment contains experiment-level metadata, including BioProject.
// We implement a custom UnmarshalXML to pick out the <EXTERNAL_ID namespace="BioProject"> value.
type Experiment struct {
	XMLName    xml.Name          `xml:"EXPERIMENT"`
	Accession  string            `xml:"accession,attr"`
	Title      string            `xml:"TITLE,omitempty"`
	Library    LibraryDescriptor `xml:"DESIGN>LIBRARY_DESCRIPTOR"`
	Study      studyRef          `xml:"STUDY_REF"`
	BioProject string            `xml:"-"`
}

// UnmarshalXML decodes the Experiment element, then scans STUDY_REF>IDENTIFIERS>EXTERNAL_ID
// for namespace="BioProject" and stores its inner text into BioProject.
func (e *Experiment) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	type rawExp Experiment
	var aux rawExp

	if err := d.DecodeElement(&aux, &start); err != nil {
		return err
	}
	*e = Experiment(aux)

	for _, ex := range e.Study.ExternalIDs {
		if ex.Namespace == "BioProject" {
			e.BioProject = strings.TrimSpace(ex.Value)
			break
		}
	}
	return nil
}

// LibraryDescriptor contains information about library preparation
type LibraryDescriptor struct {
	Strategy  string `xml:"LIBRARY_STRATEGY"`
	Source    string `xml:"LIBRARY_SOURCE"`
	Selection string `xml:"LIBRARY_SELECTION"`
}

// Sample contains sample-level metadata
type Sample struct {
	Accession   string            `xml:"accession,attr"`
	Identifiers []Identifier      `xml:"IDENTIFIERS>EXTERNAL_ID"`
	Attributes  []SampleAttribute `xml:"SAMPLE_ATTRIBUTES>SAMPLE_ATTRIBUTE"`
	Title       string            `xml:"TITLE,omitempty"`
}

// Identifier represents an external ID associated with a sample
type Identifier struct {
	Namespace string `xml:"namespace,attr"`
	Value     string `xml:",chardata"`
}

// SampleAttribute represents a key-value metadata pair for a sample
type SampleAttribute struct {
	Tag   string `xml:"TAG"`
	Value string `xml:"VALUE"`
}

// RunSet contains a collection of sequencing runs
type RunSet struct {
	Runs []Run `xml:"RUN"`
}

// Run contains information about a single sequencing run
type Run struct {
	Accession   string `xml:"accession,attr"`
	TotalSpots  string `xml:"total_spots,attr"`
	TotalBases  string `xml:"total_bases,attr"`
	LoadDate    string `xml:"load_date,attr,omitempty"`
	ReleaseDate string `xml:"published,attr,omitempty"`
}

// Platform contains information about the sequencing platform
type Platform struct {
	Illumina struct {
		Instrument string `xml:"INSTRUMENT_MODEL"`
	} `xml:"ILLUMINA"`
}

// Organization contains information about the submitting organization
type Organization struct {
	Name string `xml:"Name,omitempty"`
	Type string `xml:"Type,omitempty"`
}

// extractSampleValue searches for a specific attribute in the sample attributes
func extractSampleValue(attrs []SampleAttribute, key string) string {
	for _, attr := range attrs {
		if strings.EqualFold(attr.Tag, key) {
			return attr.Value
		}
	}
	return ""
}

// extractIdentifier searches for a specific identifier by namespace
func extractIdentifier(ids []Identifier, ns string) string {
	for _, id := range ids {
		if id.Namespace == ns {
			return id.Value
		}
	}
	return ""
}

func fetchAllIDs(query string, api_key string) ([]string, error) {
	params := url.Values{}
	params.Set("db", "sra")
	params.Set("term", query)
	params.Set("retmode", "xml")
	params.Set("retmax", "100000")
	esearchURL := "https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esearch.fcgi?" + params.Encode()
	if api_key != "" {
		esearchURL += "&api_key=" + api_key
	}
	resp, err := http.Get(esearchURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result ESearchResult
	if err := xml.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result.IdList.Ids, nil
}

func fetchBatch(ids []string) (ExperimentPackageSet, error) {
	var root ExperimentPackageSet
	idParam := strings.Join(ids, ",")
	url := fmt.Sprintf("https://eutils.ncbi.nlm.nih.gov/entrez/eutils/efetch.fcgi?db=sra&id=%s&retmode=xml", idParam)
	resp, err := http.Get(url)
	if err != nil {
		return root, err
	}
	defer resp.Body.Close()
	decoder := xml.NewDecoder(resp.Body)
	err = decoder.Decode(&root)
	return root, err
}

func chunkIDs(ids []string, size int) [][]string {
	var chunks [][]string
	for size < len(ids) {
		ids, chunks = ids[size:], append(chunks, ids[0:size])
	}
	return append(chunks, ids)
}

func writePackage(pkg ExperimentPackage, tsv *os.File) {
	exp := pkg.Experiment
	sample := pkg.Sample

	biosample := extractIdentifier(sample.Identifiers, "BioSample")
	bioproject := exp.BioProject
	if bioproject == "" {
		bioproject = extractIdentifier(sample.Identifiers, "BioProject")
		if bioproject == "" {
			bioproject = extractSampleValue(sample.Attributes, "bioproject")
		}
	}

	collDate := extractSampleValue(sample.Attributes, "collection_date")
	geoLoc := extractSampleValue(sample.Attributes, "geo_loc_name")
	pop := extractSampleValue(sample.Attributes, "ww_population")

	submitter := pkg.Organization.Name
	if submitter == "" {
		submitter = extractSampleValue(sample.Attributes, "submitter")
		if submitter == "" {
			submitter = extractSampleValue(sample.Attributes, "submitted_by")
			if submitter == "" {
				submitter = extractSampleValue(sample.Attributes, "center_name")
			}
		}
	}

	for _, run := range pkg.RunSet.Runs {
		releaseDate := run.ReleaseDate
		loadDate := run.LoadDate
		if releaseDate == "" {
			releaseDate = pkg.ReleaseDate
		}
		if loadDate == "" {
			loadDate = pkg.LoadDate
		}
		tsv.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			run.Accession,
			bioproject,
			biosample,
			submitter,
			collDate,
			geoLoc,
			pop,
			run.TotalSpots,
			releaseDate,
			loadDate))
	}
}

func main() {
	// Maximum number of retries for each goroutine
	const maxRetries = 7

	term := flag.String("term", "sars-cov-2 wastewater", "Search term")
	api_key := flag.String("api-key", "", "NCBI API key to increase requests, it increases speed but is not required.")
	startDate := flag.String("start", "2024/09/15", "Start date (yyyy/mm/dd)")
	endDate := flag.String("end", "2030/12/31", "End date (yyyy/mm/dd)")
	flag.Parse()

	// Time the function
	start := time.Now()
	defer func() {
		log.Printf("Total time of main function: %.2fs", time.Since(start).Seconds())
	}()

	ts := time.Now().Format("06.01.02.15.04")
	tsvFile := fmt.Sprintf("parsed_metadata.%s.tsv", ts)

	query := fmt.Sprintf("(%s) AND (\"%s\"[PDAT] : \"%s\"[PDAT])", *term, *startDate, *endDate)
	ids, err := fetchAllIDs(query, *api_key)
	if err != nil {
		log.Fatalf("Failed to retrieve IDs: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Found %d IDs\n", len(ids))

	batches := chunkIDs(ids, batchSize)
	total := int32(len(batches))

	// Create the DNA-style progress bar. Header “Fetching Batches” can be replaced with "" if you want no header.
	pb := polybar.New("", "Fetching Batches")
	pb.Start(int(total))

	var completed int32
	results := make(chan ExperimentPackageSet, len(batches))

	// Launch a single goroutine to monitor `completed` and update the bar.
	go func() {
	    last := int32(0)
	    for {
	        done := atomic.LoadInt32(&completed)

	        // Only call pb.SetProgress when done > last, so we skip the redundant 0→0 step.
	        if done > last {
	            pb.SetProgress(int(done))
	            last = done
	        }

	        if done >= total {
	            break
	        }
	        time.Sleep(50 * time.Millisecond)
	    }
	}()


	var wg sync.WaitGroup
	sem := make(chan struct{}, maxWorkers)

	for _, batch := range batches {
		wg.Add(1)
		sem <- struct{}{}

		go func(idList []string) {
			defer wg.Done()
			defer func() { <-sem }()

			var pkgSet ExperimentPackageSet
			var err error

			for attempt := 0; attempt < maxRetries; attempt++ {
				if attempt > 0 {
					sleepDuration := time.Duration(math.Pow(2, float64(attempt))) * time.Second
					time.Sleep(sleepDuration)
					if attempt > 5 {
						fmt.Fprintf(os.Stderr, "Retrying batch after %v...\n", sleepDuration)
					}
				}
				pkgSet, err = fetchBatch(idList)
				if err == nil {
					results <- pkgSet
					atomic.AddInt32(&completed, 1)
					return
				}
			}
			fmt.Fprintf(os.Stderr, "Failed batch after retries: %v\n", err)
		}(batch)
	}

	wg.Wait()
	// All workers have finished; ensure progress is at 100% and finalize the bar.
	pb.Finish()

	close(results)

	tsv, err := os.Create(tsvFile)
	if err != nil {
		log.Fatalf("Failed to create TSV: %v", err)
	}
	defer tsv.Close()
	tsv.WriteString("RunAccession\tBioProject\tBioSample\tSubmitter\tCollectionDate\tLocation\tPopulation\tTotalSpots\tReleaseDate\tLoadDate\n")

	for pkgSet := range results {
		for _, pkg := range pkgSet.Packages {
			writePackage(pkg, tsv)
		}
	}
	fmt.Fprintf(os.Stderr, "Saved parsed metadata to %s\n", tsvFile)
}
