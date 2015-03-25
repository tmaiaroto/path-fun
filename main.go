package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"github.com/fatih/color"
	"github.com/jeffail/tunny"
	pathfinding "github.com/tmaiaroto/gopathfinding"
	"io/ioutil"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
)

// Note: Unless the stop and start characters are different, they will both be the same color in the output when colorizing.
// The output legend can easily be changed by modifying these constants (however, this it would break the tests).
const STOP_CHAR string = "#"
const START_CHAR string = "#"
const PATH_CHAR string = "*"
const SPACE_CHAR string = "."

type pathLegend struct {
	rawData []string
	rows    int
	cols    int
	stops   int
	maps    []pathfinding.MapData
}

var numCPUs int

func main() {
	numCPUs = runtime.NumCPU()
	runtime.GOMAXPROCS(numCPUs)
	args := os.Args[1:]

	// Make sure we have at least the input file.
	if len(args) < 1 {
		color.Red("Not enough arguments passed.")
		return
	}

	// Load the character data from the input file.
	err, l := newLegend(args[0])
	if err != nil {
		color.Red(err.Error())
		return
	}

	log.Println(len(l.maps))

	// If there was a second argument, we're going to assume it was the output file, write there.
	if len(args) > 1 {
		// We could output ANSI colors to a text file too of course, but we won't so the tests will pass.
		outputToFile(args[1], l.solve(false))
	} else {
		// Otherwise, allow the program to run and show us the result in stdout.
		fmt.Println(l.solve(true))
	}

}

// Loads the map data from a character representation from file on disk.
func newLegend(inputFile string) (error, pathLegend) {
	var f *os.File
	var err error
	var l = pathLegend{}

	// Open the input file.
	f, err = os.Open(inputFile)
	defer f.Close()
	if err != nil {
		return err, l
	}

	// Scan each line in order to build a map.
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		rowData := scanner.Text()
		l.rows++
		if l.cols > 0 && l.cols != len(rowData) || len(rowData) == 0 {
			return errors.New("The input file does not contain an even number of columns from row to row or contains an empty line."), l
		}
		l.cols = len(rowData)
		// Add the raw data to the slice, we'll decode it once we have it all and have verified it has enough data to continue.
		l.rawData = append(l.rawData, rowData)
	}

	// If something went wrong scanning the file.
	if err := scanner.Err(); err != nil {
		return err, l
	}

	// Be nice and let the user know there isn't enough data
	if l.rows == 0 || l.cols == 0 {
		return errors.New("The input file does not have enough data to calculate a path."), l
	}

	// Get the number of stops.
	for _, row := range l.rawData {
		for _, char := range []rune(row) {
			if string(char) == STOP_CHAR {
				l.stops++
			}
		}
	}
	// Remember, don't count the first start as a stop.
	l.stops = (l.stops - 1)
	// Make sure there's stops.
	if l.stops < 1 {
		return errors.New("There seem to be no stops along this path."), l
	}

	// Set the MapData for each set of points.
	l.maps = make([]pathfinding.MapData, l.stops)
	for i := 0; i < l.stops; i++ {
		err := l.setMapData(i)
		if err != nil {
			return err, l
		}
	}

	return nil, l
}

// Sets the MapData struct for a single point to point
func (l pathLegend) setMapData(startFrom int) error {
	// prevent out of range fatal errors and show something a little more descriptive
	if startFrom > len(l.maps) {
		return errors.New("Trying to start from a stop beyond the number of stops found.")
	}
	l.maps[startFrom] = *pathfinding.NewMapData(l.rows, l.cols)
	startFound := false
	stopFound := false

	i := 0
	for rI, row := range l.rawData {
		for cI, char := range []rune(row) {
			switch string(char) {
			case SPACE_CHAR:
				l.maps[startFrom][rI][cI] = pathfinding.LAND
			case STOP_CHAR:
				if !startFound {
					// Figure out from where to start.
					if i == startFrom {
						l.maps[startFrom][rI][cI] = pathfinding.START
						startFound = true
					}
				} else {
					if !stopFound {
						l.maps[startFrom][rI][cI] = pathfinding.STOP
						stopFound = true
					}
				}
				i++
			}
		}
	}
	return nil
}

// Determines the shortest path and sets pathLegend.solved which can be displayed or saved to file, etc.
func (l pathLegend) solve(colored bool) string {
	//var result string
	var solved string
	var buffer bytes.Buffer

	startChar := START_CHAR
	stopChar := STOP_CHAR
	spaceChar := SPACE_CHAR
	pathChar := PATH_CHAR
	if colored {
		startChar = color.GreenString("%s", startChar)
		stopChar = color.RedString("%s", stopChar)
		pathChar = color.YellowString("%s", pathChar)
		spaceChar = color.WhiteString("%s", spaceChar)
	}
	// Make a mapping to easily get the proper colorized or plain output for a character.
	colorMap := make(map[string]string, 4)
	colorMap[START_CHAR] = startChar
	colorMap[STOP_CHAR] = stopChar
	colorMap[SPACE_CHAR] = spaceChar
	colorMap[PATH_CHAR] = pathChar

	numJobs := len(l.maps)
	wg := new(sync.WaitGroup)
	wg.Add(numJobs)
	// There are multiple paths that need to be merged into one solved path legend. Start by creating a text representation of each point to point path.
	var legendOutputParts = make([]string, numJobs)

	// Find the shortest paths in a worker pool to take advantage of the number of CPUs on the machine and process these in parallel, but wait for them all to complete.
	pool, _ := tunny.CreatePool(numCPUs, func(object interface{}) interface{} {
		mapData := object.(pathfinding.MapData)
		graph := pathfinding.NewGraph(&mapData)
		nodes := pathfinding.Astar(graph)

		var chars []string

		for i, row := range mapData {
			for j, cell := range row {
				added := false
				for nI, node := range nodes {
					if node.X == i && node.Y == j {
						if nI == 0 {
							chars = append(chars, colorMap[START_CHAR])
						} else if nI+1 == len(nodes) {
							chars = append(chars, colorMap[STOP_CHAR])
						} else {
							chars = append(chars, colorMap[PATH_CHAR])
						}

						added = true
						break
					}
				}
				if !added {
					switch cell {
					case pathfinding.LAND:
						chars = append(chars, colorMap[SPACE_CHAR])
					case pathfinding.START:
						chars = append(chars, colorMap[START_CHAR])
					case pathfinding.STOP:
						chars = append(chars, colorMap[STOP_CHAR])
					default:
						chars = append(chars, " ")
					}
				}
			}
			chars = append(chars, "\n")
		}
		return strings.Join(chars, "")

	}).Open()
	defer pool.Close()

	// Send the work and wait
	for i := 0; i < numJobs; i++ {
		go func(index int) {
			value, _ := pool.SendWork(l.maps[index])
			legendOutputParts[index] = value.(string)
			wg.Done()
		}(i)
	}
	wg.Wait()

	// If there's nothing to return (which shouldn't be the case), just return the empty string. No solvable paths? No problem.
	if len(legendOutputParts) == 0 {
		return solved
	}

	// Then merge, starting with the first legend.
	var lRows []string
	mergeRows := func(rows []string, firstRows []string) []string {
		var newRows []string
		for rI, row := range rows {
			// There are new line at the end of the input files. We don't want them. Well, we actually want one of them to match the test which we get from the firstLegendRows split.
			if row != "" {
				for cI, char := range []rune(row) {
					mergedChar := string(firstRows[rI][cI])
					// Don't replace characters with space characters, else the path would be moving in a sense. We want to see all paths combined.
					if string(char) != SPACE_CHAR && string(char) != " " {
						if string(firstRows[rI][cI]) == SPACE_CHAR {
							mergedChar = string(char)
						} else {
							mergedChar = string(firstRows[rI][cI])
						}
					}
					// write the updated/merged character
					buffer.WriteString(colorMap[mergedChar])
				}
			}
			newRows = append(newRows, buffer.String())
			buffer.Reset()
		}
		return newRows
	}

	lRows = strings.Split(legendOutputParts[0], "\n")
	for i := 1; i < len(legendOutputParts); i++ {
		current := strings.Split(legendOutputParts[i], "\n")
		// Keep getting a copy of the updated/new slice
		lRows = mergeRows(current, lRows)
	}
	return strings.Join(lRows, "\n")
}

// Save some output to a file on disk
func outputToFile(path string, data string) {
	err := ioutil.WriteFile(path, []byte(data), 0644)
	if err != nil {
		log.Fatalln(err)
	}
}
