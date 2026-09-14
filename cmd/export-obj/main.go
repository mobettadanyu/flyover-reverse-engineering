package main

import (
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/retroplasma/flyover-reverse-engineering/pkg/fly"
	"github.com/retroplasma/flyover-reverse-engineering/pkg/fly/c3m"
	"github.com/retroplasma/flyover-reverse-engineering/pkg/fly/exp"
	"github.com/retroplasma/flyover-reverse-engineering/pkg/mps"
	"github.com/retroplasma/flyover-reverse-engineering/pkg/mps/config"
	"github.com/retroplasma/flyover-reverse-engineering/pkg/mth"
	"github.com/retroplasma/flyover-reverse-engineering/pkg/oth"
	"github.com/retroplasma/flyover-reverse-engineering/pkg/web"
)

var l = log.New(os.Stderr, "", 0)

func printUsage(msg string) {
	if msg != "" {
		l.Println("Error:", msg)
	}
	l.Println("Usage", os.Args[0], "[lat] [lon] [zoom] [tryXY] [tryH] [[--parallel]]")
	l.Println()
	l.Println("  Name    Description       Example")
	l.Println("  --------------------------------------")
	ex := []string{"34.007603", "-118.499741", "20", "3", "40"}
	l.Println("  lat     Latitude         ", ex[0])
	l.Println("  lon     Longitude        ", ex[1])
	l.Println("  zoom    Zoom (~ 13-20)   ", ex[2])
	l.Println("  tryXY   Area scan        ", ex[3])
	l.Println("  tryH    Altitude scan    ", ex[4])
	l.Println("Example:", os.Args[0], ex[0], ex[1], ex[2], ex[3], ex[4])
	os.Exit(1)
}

var scraped_regions_to_ignore []string
func contains(slice []string, target string) bool {
	for _, item := range slice {
		if item == target {
			return true
		}
	}
	return false
}

func main() {

	var err error
	aReq := make([]string, 0)
	aOpt := make([]string, 0)
	for _, a := range os.Args[1:] {
		if !strings.HasPrefix(a, "--") {
			aReq = append(aReq, a)
		} else {
			aOpt = append(aOpt, a)
		}
	}
	if len(os.Args) == 1 {
		printUsage("")
	}
	if len(aReq) != 5 {
		printUsage("Invalid argument number")
	}
	lat, err := strconv.ParseFloat(aReq[0], 64)
	if err != nil {
		printUsage("Invalid lat")
	}
	lon, err := strconv.ParseFloat(aReq[1], 64)
	if err != nil {
		printUsage("Invalid lon")
	}
	zoom, err := strconv.ParseInt(aReq[2], 10, 32)
	if err != nil {
		printUsage("Invalid zoom")
	}
	tryXY, err := strconv.ParseInt(aReq[3], 10, 32)
	if err != nil {
		printUsage("Invalid tryXY")
	}
	tryH, err := strconv.ParseInt(aReq[4], 10, 32)
	if err != nil {
		printUsage("Invalid tryH")
	}
	parallel := false
	for _, a := range aOpt {
		switch a {
		case "--parallel":
			parallel = true
		default:
			printUsage("Unknown param: " + a)
		}
	}

	cache := mps.Cache{Enabled: true, Directory: "./cache"}
	err = cache.Init()
	oth.CheckPanic(err)
	config, err := config.FromJSONFile("./config.json")
	oth.CheckPanic(err)
	if !config.IsValid() {
		fmt.Fprintln(os.Stderr, "please set values in config.json")
		os.Exit(1)
	}
	ctx, err := getContext(cache, config)
	oth.CheckPanic(err)

	z := int(zoom)
	x, y := mth.LatLonToTileTMS(z, lat, lon)
	
	for {
		p, err := ctx.findPlace(lat, lon)
		oth.CheckPanic(err)
		l.Println(p.Name, p.Radius, math.Sqrt(math.Pow(p.Lat, 2) + math.Pow(p.Lon, 2)), p.Lat, p.Lon)

		// add to list of already scraped regions (ignore)
		scraped_regions_to_ignore = append(scraped_regions_to_ignore, p.Name)

		// for _, v := range ctx.AltitudeManifest.Triggers {
		// 	dist := math.Sqrt(math.Pow(lat-v.Lat, 2) + math.Pow(lon-v.Lon, 2))
		// 	if dist < 5 { // adjust as needed — degrees, so 5 is generous
		// 		l.Println("nearby:", v.Name, "radius:", v.Radius, "dist:", dist)
		// 	}
		// }

		exportDir := fmt.Sprintf("./downloaded_files/obj/%f-%f-%d-%d-%d", lat, lon, zoom, tryXY, tryH)
		err = os.MkdirAll(exportDir, 0755)
		oth.CheckPanic(err)

		xp := 0
		export, err := exp.New(exportDir, "exp_")
		oth.CheckPanic(err)
		defer func() {
			oth.CheckPanic(export.Close())
		}()

		c3m.DisableLogs()

		// semaphore settings
		dln := 1
		if parallel {
			dln = 16
		}
		sem := make(chan int, dln)
		var wg sync.WaitGroup

		// exporter for decoded tiles
		ex, exDone := make(chan c3m.C3M, dln), make(chan int)
		go func() {
			for tile := range ex {
				oth.CheckPanic(export.Next(tile, fmt.Sprintf("%d", xp)))
				xp++
			}
			exDone <- 1
		}()

		// Loop over the area and altitude grid. The old C3MM (style 14) octree index
		// that used to tell us which tiles exist is no longer served by Apple, so we
		// probe the C3M tiles (style 15) directly: a tile that has no data returns an
		// empty body, which getTileIfPresent reports as "not present" and we skip.
		for dx := -tryXY; dx <= tryXY; dx++ {
			for dy := -tryXY; dy <= tryXY; dy++ {
				for h := 0; h < int(tryH); h++ {
					xn := x + int(dx)
					yn := y + int(dy)

					// async get tile
					sem <- 1
					wg.Add(1)
					dx, dy, h := dx, dy, h
					go func() {
						defer wg.Done()
						defer func() { <-sem }()
						tile, found, err := ctx.getTileIfPresent(p, z, yn, xn, h)
						if err != nil {
							l.Println("Error at", dx, dy, "h =", h, ":", err)
							return
						}
						if !found {
							return
						}
						l.Println("Exporting", dx, dy, "h =", h)
						ex <- tile
					}()
				}
			}
		}
		wg.Wait() // wait for all tile loads to finish
		close(ex) // no more tiles sent to exporter
		<-exDone  // wait till all tiles are exported
		l.Println(xp, "exported")
		if xp != 0{
			break
		}
	}
}

// getTileIfPresent fetches and parses the C3M tile at the given coordinates.
// It returns found=false (and no error) when there is simply no tile at those
// coordinates, which Apple signals with a 404, an empty body, or a JPEG
// placeholder. A non-nil error is only returned for unexpected transport
// failures so a scan over an area degrades gracefully instead of aborting.
func (ctx *context) getTileIfPresent(p fly.Trigger, z, y, x, h int) (c3m.C3M, bool, error) {
	yn := mth.TileCountPerAxis(z) - 1 - y // invert y
	url := fmt.Sprintf("%s?style=%d&v=%d&region=%d&x=%d&y=%d&z=%d&h=%d",
		ctx.URLPrefixC3m, mps.ResourceManifest_StyleConfig_C3M, p.Version, p.Region, x, yn, z, h)

	data, err := ctx.get(url)
	if err != nil {
		// 404 (no such tile) and JPEG placeholders are expected during a scan.
		if strings.Contains(err.Error(), "http status 404") || err.Error() == "received jpeg" {
			return c3m.C3M{}, false, nil
		}
		return c3m.C3M{}, false, err
	}
	// An empty 200 response means there is no tile at these coordinates.
	if len(data) < 5 {
		//fmt.Print("Empty\n")
		return c3m.C3M{}, false, nil
	}
	tile, err := c3m.Parse(data)
	if err != nil {
		// Unparseable payloads are treated as "no tile" but surfaced so genuine
		// format regressions remain visible.
		return c3m.C3M{}, false, fmt.Errorf("parse: %w", err)
	}
	return tile, true, nil
}

func (ctx context) findPlace(lat, lon float64) (fly.Trigger, error) {
	// radius non spherical yet
	minDist, minPlace := math.Inf(1), fly.Trigger{}

	for _, v := range ctx.AltitudeManifest.Triggers {
		dist := math.Sqrt(math.Pow(lat-v.Lat, 2) + math.Pow(lon-v.Lon, 2))
		// radius can overlap. ignored for now
		if dist <= v.Radius && dist < minDist && !contains(scraped_regions_to_ignore, v.Name){
			minDist, minPlace = dist, v
		}
	}
	if minDist == math.Inf(1) {
		return fly.Trigger{}, errors.New("minPlace not found")
	}
	return minPlace, nil
}

func (ctx context) get(url string) ([]byte, error) {
	authURL, err := ctx.Context.AuthContext.AuthURL(url)
	if err != nil {
		return nil, err
	}
	return get(authURL)
}

func get(url string) (data []byte, err error) {
	jpgErr := errors.New("received jpeg")
	data, err = web.GetWithCheck(url, func(res *http.Response) (err error) {
		// fail early if there's a jpeg, which is sometimes sent if there's no c3m(m)
		if res.Header.Get("content-type") == "image/jpeg" {
			err = jpgErr
		}
		return
	})
	return
}

type context struct {
	Context          mps.Context
	AltitudeManifest fly.AltitudeManifest
	URLPrefixC3mm    string
	URLPrefixC3m     string
}

func getContext(cache mps.Cache, config config.Config) (m context, err error) {
	ctx, err := mps.Init(cache, config)
	if err != nil {
		return
	}
	am, err := fly.GetAltitudeManifest(cache, ctx.ResourceManifest)
	if err != nil {
		return
	}

	c3mmURLPrefix, err := ctx.ResourceManifest.URLPrefixFromStyleID(mps.ResourceManifest_StyleConfig_C3MM_1)
	if err != nil {
		return
	}
	c3mURLPrefix, err := ctx.ResourceManifest.URLPrefixFromStyleID(mps.ResourceManifest_StyleConfig_C3M)
	if err != nil {
		return
	}

	m = context{ctx, am, c3mmURLPrefix, c3mURLPrefix}
	return
}
