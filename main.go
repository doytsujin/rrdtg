package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gizak/termui"
	"github.com/ziutek/rrd"
	//"github.com/valobanov/rrd"
)

var layouts = []int{1, 2, 3, 4, 6, 12}

// aggregated info for all rrd files in scope
var all = struct {
	lastUpdate   int64           // maximum last uodate
	cfs          map[string]bool // all used cfs
	minSecPerRow uint            // defines maximumresolution
}{
	0,
	map[string]bool{cfErr: true}, // to show for erroneous datasets
	math.MaxUint32,
}

const cfErr = "err"
const timeFormat = "2006-01-02 15:04:05"

const timeZoomFractions = 256

var timeZoom uint = timeZoomFractions
var vScroll = 0
var start time.Time
var end time.Time
var layout = 0
var mode = 0

var lastRenderDur time.Duration

func timeZoomResolution() time.Duration {
	return time.Second * time.Duration(timeZoom*all.minSecPerRow/timeZoomFractions)
}

type uiLog struct {
	*termui.Par
}

func (l uiLog) Write(p []byte) (n int, err error) {
	l.Par.Text += string(p)
	return
}

func (l uiLog) clear() {
	l.Par.Text = ""
	return
}

var log = uiLog{termui.NewPar("")}

func main() {
	flag.Parse()

	fmt.Println("RRD Term Grapher")
	globs := flag.Args()
	if len(globs) == 0 {
		globs = []string{"*.rrd"}
	}
	err, errCode := parseGlobs(globs)
	if err != nil {
		fmt.Println("Error:", err, errCode)
		os.Exit(errCode)
	}
	err, errCode = getFilenamesFromDirs()
	if err != nil {
		fmt.Println("Error:", err, errCode)
		os.Exit(errCode)
	}

	err, errCode = getDatasetsFromFiles()
	if err != nil {
		fmt.Println("Error:", err, errCode)
		os.Exit(errCode)
	}

	fmt.Println("Files: ", files)
	fmt.Println("Dirs: ", dirs)

	if len(datasets) == 0 {
		fmt.Println("No files to render")
		os.Exit(0)
	}

	err = termui.Init()
	if err != nil {
		panic(err)
	}
	defer termui.Close()

	end = time.Unix(all.lastUpdate, 0)
	renderLoop()
}

type rrdFile struct {
	file       string
	lastUpdate int64
	ds         map[string]bool
	cf         map[string]bool
	err        error
}

type dataset struct {
	*rrdFile
	ds string
	cf string
}

var (
	datasets []*dataset
	files    []string
	dirs     []string
)

// files or dirs (non recursive) list
func parseGlobs(globs []string) (error, int) {

	for _, g := range globs {
		f, err := filepath.Glob(g)
		if err != nil {
			return err, 11
		}
		for _, f := range f {
			info, err := os.Stat(f)
			if err != nil {
				return err, 12
			}
			if info.IsDir() {
				dirs = append(dirs, f)
			} else {
				files = append(files, f)
			}
		}
	}
	return nil, 0
}

func getFilenamesFromDirs() (error, int) {

	for _, d := range dirs {
		f, err := os.Open(d)
		if err != nil {
			return err, 21
		} else {
			infos, err := f.Readdir(-1)
			if err != nil {
				return err, 22
			}
			for _, i := range infos {
				if !i.IsDir() {
					files = append(files, filepath.Join(d, i.Name()))
				}
			}
		}
	}

	return nil, 0
}

func getDatasetsFromFiles() (error, int) {
	for _, file := range files {
		info, err := rrd.Info(file)
		var rrd *rrdFile
		if err != nil {
			rrd = &rrdFile{file, 0, nil, nil, err}
			datasets = append(datasets, &dataset{rrd, "", cfErr})
		} else {
			step := info["step"].(uint)
			pdp_per_row := info["rra.pdp_per_row"].([]interface{})
			for _, pdpPerRowI := range pdp_per_row {
				pdpPerRow := pdpPerRowI.(uint)
				secPerRow := step * pdpPerRow
				if all.minSecPerRow > secPerRow {
					all.minSecPerRow = secPerRow
				}
			}

			lastUpdate := int64(info["last_update"].(uint))
			if all.lastUpdate < lastUpdate {
				all.lastUpdate = lastUpdate
			}
			rrd = &rrdFile{file, lastUpdate, make(map[string]bool), make(map[string]bool), nil}
			for ds, _ := range info["ds.type"].(map[string]interface{}) {
				rrd.ds[ds] = true
			}
			for _, cf := range info["rra.cf"].([]interface{}) {
				rrd.cf[cf.(string)] = true
			}

			// dataset is ds per cs
			for ds, _ := range rrd.ds {
				for cf, _ := range rrd.cf {
					all.cfs[cf] = true
					datasets = append(datasets, &dataset{rrd, ds, cf})
				}
			}
		}
	}
	return nil, 0
}

func renderLoop() {
	for {

		cells := layouts[layout]
		if vScroll < 0 {
			vScroll = 0
		}

		time0 := time.Now()

		grid := renderInit()

		var dsFiltered []*dataset
		for _, ds := range datasets {
			if all.cfs[ds.cf] {
				dsFiltered = append(dsFiltered, ds)
			}
		}
		dsIdx := vScroll * cells
		for i := 0; i < cells*cells && dsIdx < len(dsFiltered); i++ {
			if all.cfs[dsFiltered[dsIdx].cf] {
				idx := i + vScroll*cells
				widget := &grid.Rows[i/cells].Cols[i%cells].Widget
				// may replace graph with paragraph in case of error
				*widget = renderDataset(dsFiltered[idx], (*widget).(*termui.LineChart), idx)
				dsIdx++
			}
		}

		grid.Align()
		renderHeader(grid)
		termui.Render(log, grid)

		lastRenderDur = time.Since(time0)

		handleInput()
	}
}

func renderInit() (grid *termui.Grid) {
	log.Width = termui.TermWidth()
	log.Height = 5

	cells := layouts[layout]
	grid = termui.NewGrid()
	grid.Y = 5
	rows := make([]*termui.Row, cells)
	for i := 0; i < cells; i++ {
		cols := make([]*termui.Row, cells)
		for j := 0; j < cells; j++ {
			graph := termui.NewLineChart()
			if mode == 1 {
				graph.Mode = "dot"
				//graph.DotStyle = 'o'
			}
			graph.Height = (termui.TermHeight() - 5) / cells
			if graph.Height < 3 {
				graph.Height = 3
			}
			graph.AxesColor = termui.ColorWhite
			graph.LineColor = termui.ColorGreen | termui.AttrBold
			cols[j] = termui.NewCol(12/cells, 0, graph)
		}
		rows[i] = termui.NewRow(cols...)
	}

	grid.AddRows(rows...)

	grid.Width = termui.TermWidth()
	grid.Align()
	return
}

func renderHeader(grid *termui.Grid) {
	cfList := make([]string, len(all.cfs))
	i := 0
	for cf, v := range all.cfs {
		if v {
			cfList[i] = cf + "+"
		} else {
			cfList[i] = cf + "-"
		}
		i++
	}
	sort.Strings(cfList)
	log.Border.Label = fmt.Sprintln("RRD Term Grapher [q]uit] [h]elp]", "res:", fmt.Sprint(timeZoomResolution(), "/", time.Duration(all.minSecPerRow)*time.Second), "end:", end.Format(timeFormat), "v-scroll:", vScroll, "layout:", layouts[layout], "cf: "+strings.Join(cfList, " "), lastRenderDur)
}

func renderDataset(dataset *dataset, graph *termui.LineChart, idx int) (widget termui.GridBufferer) {
	if dataset.err == nil {
		labelYSpace := 10
		_, _, width, _ := graph.InnerBounds()
		pointCount := (width - labelYSpace)
		if graph.Mode == "braille" {
			pointCount *= 2
		}
		if pointCount <= 0 {
			pointCount = 1
		}
		var x rrd.Exporter
		x.Def("x", dataset.file, dataset.ds, dataset.cf)

		start = end.Add(-time.Duration(pointCount) * timeZoomResolution())
		x.XportDef("x", "x")

		x.CDef("t", "x,POP,TIME")
		x.XportDef("t", "t")

		if pointCount > 10 {
			x.SetMaxRows(uint(pointCount))
		} else {
			x.SetMaxRows(10)
		}
		xportRes, err := x.Xport(start, end, 0)
		if err != nil {
			widget = makeErrPar(err, idx, dataset.file, graph.Height)
		} else {
			data := make([]float64, pointCount)
			dataLabels := make([]string, pointCount)
			endF := float64(end.Unix())
			resF := float64(all.minSecPerRow)
			for i := 0; i < pointCount; i++ {
				x := xportRes.ValueAt(0, i*xportRes.RowCnt/pointCount)

				dataLabels[i] = fmt.Sprint((xportRes.ValueAt(1, i*xportRes.RowCnt/pointCount) - endF) / resF)
				if math.IsNaN(x) {
					data[i] = 0
				} else {
					data[i] = x
				}

			}

			xportRes.FreeValues()
			graph.DataLabels = dataLabels
			graph.Data = data
			graph.Border.Label = fmt.Sprintln(idx, filepath.Base(dataset.file), dataset.ds, dataset.cf, xportRes.RowCnt, len(data), end.Sub(start))

			//			fmt.Fprintln(log, data)

			widget = graph
		}
	} else {
		widget = makeErrPar(dataset.err, idx, dataset.file, graph.Height)
	}
	return widget
}

func makeErrPar(err error, idx int, label string, height int) termui.GridBufferer {
	par := termui.NewPar(fmt.Sprintln("Error:", err))
	par.Height = height
	par.Border.Label = fmt.Sprintln(idx, label)
	par.TextFgColor = termui.ColorRed
	return par
}

func switchAllCf(cf string) {
	if _, ok := all.cfs[cf]; ok {
		all.cfs[cf] = !all.cfs[cf]
	}

}

func handleInput() {
	prevLayout := layout
	select {
	case e := <-termui.EventCh():

		switch e.Type {
		case termui.EventKey:

			switch e.Ch {
			case 0:
				switch e.Key {
				case termui.KeyPgup:
					vScroll -= layouts[layout]
				case termui.KeyPgdn:
					vScroll += layouts[layout]
				case termui.KeyArrowUp:
					vScroll--
				case termui.KeyArrowDown:
					vScroll++
				case termui.KeyArrowLeft:
					end = end.Add(-timeZoomResolution() * 10)
				case termui.KeyArrowRight:
					end = end.Add(timeZoomResolution() * 10)
				case termui.KeyF1:
					switchAllCf("AVERAGE")
				case termui.KeyF2:
					switchAllCf("MAX")
				case termui.KeyF3:
					switchAllCf("MIN")
				case termui.KeyF4:
					switchAllCf(cfErr)
				case termui.KeySpace:
					mode = (mode + 1) % 2
				}

			case '1':
				layout = 0
			case '2':
				layout = 1
			case '3':
				layout = 2
			case '4':
				layout = 3
			case '5':
				layout = 4
			case '6':
				layout = 5
			case 'h':
				log.clear()
				fmt.Fprintln(log, "layouts:[1-5] cfs:[F1-F4] v-scr:[PgUp,PgDn,UpArr,DnArr] scale:[+/-] end time:[L/R Arr]")
				fmt.Fprint(log, "time: [", start.Format(timeFormat), " - ", end.Format(timeFormat), "] files: ", len(files), "\n")
				fmt.Fprint(log, "x-axes are in min resolutions (", time.Duration(all.minSecPerRow)*time.Second, ") from end time\n")
			case 'q':
				termui.Close()
				os.Exit(0)
			case '=':
				timeZoom /= 2
				if timeZoom == 0 {
					timeZoom = 1
				}
			case '-':
				timeZoom *= 2
			case '/':
				timeZoom = timeZoomFractions
			}

		case termui.EventResize:
		}
	}
	if prevLayout != layout {
		vScroll = vScroll * layouts[prevLayout] / layouts[layout]
	}

}
