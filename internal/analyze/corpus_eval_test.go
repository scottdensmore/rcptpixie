//go:build eval

package analyze_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sample is one receipt with its ground truth. Each is rendered twice — once
// keeping its text layer, once flattened to an image at the same DPI the
// rasterizer uses — so any difference in the result is attributable to the path
// the document took and nothing else.
type sample struct {
	Name      string
	Lines     []string
	Hotel     bool
	StartDate string // ISO; the check-in date for a hotel
	EndDate   string // ISO; equal to StartDate unless Hotel
	Total     float64
	Vendor    string // substring match: the model may keep or drop a store number
}

// corpus deliberately varies the shape of the date, because that is the field
// under investigation: ISO, US month-first, European day-first, dotted European,
// spelled-out and abbreviated months, a two-digit year, folios carrying two
// dates, a receipt where printed/due dates compete with the service date, and
// one where the date sits at the bottom rather than the top.
var corpus = []sample{
	{
		Name:      "iso",
		Lines:     []string{"CITY HARDWARE", "441 Bridge Road", "Date: 2023-01-15", "Hammer 18.00", "Nails 4.50", "TOTAL  22.50"},
		StartDate: "2023-01-15", EndDate: "2023-01-15", Total: 22.50, Vendor: "CITY HARDWARE",
	},
	{
		Name:      "us-slash",
		Lines:     []string{"BLUE BOTTLE COFFEE", "Date: 06/03/2025", "Latte 5.75", "Croissant 4.25", "TOTAL $10.00"},
		StartDate: "2025-06-03", EndDate: "2025-06-03", Total: 10.00, Vendor: "BLUE BOTTLE",
	},
	{
		Name:      "euro-slash",
		Lines:     []string{"RESTAURANT LE JULES VERNE", "Paris, France", "Date: 15/03/2025", "TOTAL: 1.234,56 EUR"},
		StartDate: "2025-03-15", EndDate: "2025-03-15", Total: 1234.56, Vendor: "JULES VERNE",
	},
	{
		Name:      "spelled-month",
		Lines:     []string{"COMCAST BUSINESS", "Invoice Date: January 8, 2024", "Internet Service", "Amount Due: $189.99"},
		StartDate: "2024-01-08", EndDate: "2024-01-08", Total: 189.99, Vendor: "COMCAST",
	},
	{
		Name:      "short-month",
		Lines:     []string{"SHELL 4471", "12 MAR 24  14:32", "Unleaded 38.11L", "TOTAL  71.24"},
		StartDate: "2024-03-12", EndDate: "2024-03-12", Total: 71.24, Vendor: "SHELL",
	},
	{
		Name:      "two-digit-year",
		Lines:     []string{"TRADER JOES 118", "Date 11/02/23", "Groceries", "TOTAL 64.19"},
		StartDate: "2023-11-02", EndDate: "2023-11-02", Total: 64.19, Vendor: "TRADER JOE",
	},
	{
		Name: "hotel-folio",
		Lines: []string{"THE GRAND HOTEL", "500 Market Street, Chicago IL", "Guest: S. Densmore",
			"Check-In:  04/02/2025", "Check-Out: 04/06/2025", "Room 4 nights x 380.00   1520.00",
			"Taxes and fees   210.33", "TOTAL DUE  USD 1730.33"},
		Hotel: true, StartDate: "2025-04-02", EndDate: "2025-04-06", Total: 1730.33, Vendor: "GRAND HOTEL",
	},
	{
		Name: "hotel-spelled",
		Lines: []string{"SEASIDE INN", "Arrival: March 3, 2024", "Departure: March 7, 2024",
			"4 nights at 203.00", "BALANCE 812.00"},
		Hotel: true, StartDate: "2024-03-03", EndDate: "2024-03-07", Total: 812.00, Vendor: "SEASIDE INN",
	},
	{
		Name: "competing-dates",
		Lines: []string{"NORTHWIND UTILITIES", "Statement printed: 02/01/2024", "Service date: 01/15/2024",
			"Payment due: 03/01/2024", "AMOUNT 143.77"},
		StartDate: "2024-01-15", EndDate: "2024-01-15", Total: 143.77, Vendor: "NORTHWIND",
	},
	{
		Name: "thermal-style",
		Lines: []string{"*** CORNER MARKET ***", "TEL 555-0142", "--------------------", "05/22/2024  09:14 AM",
			"MILK           3.49", "BREAD          2.99", "--------------------", "TOTAL          6.48", "VISA ****1234"},
		StartDate: "2024-05-22", EndDate: "2024-05-22", Total: 6.48, Vendor: "CORNER MARKET",
	},
	{
		Name:      "date-at-bottom",
		Lines:     []string{"AIRPORT PARKING", "Lot C Space 219", "Duration 3 days", "TOTAL 96.00", "", "Issued 09/30/2024"},
		StartDate: "2024-09-30", EndDate: "2024-09-30", Total: 96.00, Vendor: "AIRPORT PARKING",
	},
	{
		Name:      "dotted-euro",
		Lines:     []string{"CAFE MOZART", "Wien, Austria", "24.12.2023", "Kaffee 4,80", "Torte 6,20", "SUMME 11,00"},
		StartDate: "2023-12-24", EndDate: "2023-12-24", Total: 11.00, Vendor: "CAFE MOZART",
	},
}

// paths returns the text-layer PDF and the rasterized image for one sample.
type paths struct{ text, scan string }

// buildCorpus renders every sample. The scan is produced at the same DPI the
// rasterizer uses, so it is the image the vision path would really receive.
func buildCorpus(t *testing.T, dir string) map[string]paths {
	t.Helper()
	for _, tool := range []string{"gs", "pdftoppm"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required to build the eval corpus", tool)
		}
	}
	out := make(map[string]paths, len(corpus))
	for _, s := range corpus {
		text := filepath.Join(dir, s.Name+"-text.pdf")
		writeTextPDF(t, text, s.Lines)

		// The same flags internal/doc uses for a scanned page. Rendering at some
		// other resolution would measure an image the tool never produces.
		prefix := filepath.Join(dir, s.Name+"-scan")
		mustRun(t, "pdftoppm", "-png", "-cropbox", "-scale-to", "1600", "-f", "1", "-l", "1", "-singlefile", text, prefix)
		scan := prefix + ".png"
		if _, err := os.Stat(scan); err != nil {
			t.Fatalf("rasterizing %s produced no page: %v", text, err)
		}
		out[s.Name] = paths{text: text, scan: scan}
	}
	return out
}

func writeTextPDF(t *testing.T, path string, lines []string) {
	t.Helper()
	var ps strings.Builder
	ps.WriteString("%!PS\n/Helvetica findfont 11 scalefont setfont\n")
	y := 720
	for _, l := range lines {
		esc := strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(l)
		fmt.Fprintf(&ps, "72 %d moveto (%s) show\n", y, esc)
		y -= 16
	}
	ps.WriteString("showpage\n")

	psPath := path + ".ps"
	if err := os.WriteFile(psPath, []byte(ps.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(psPath)
	mustRun(t, "gs", "-q", "-dNOPAUSE", "-dBATCH", "-sDEVICE=pdfwrite", "-sOutputFile="+path, psPath)
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
