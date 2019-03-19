package main

import (
	"fmt"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
	"github.com/tidwall/finn"
)

var (
	debug           bool
	version         bool
	maxDatafileSize int

	bind          string
	dir           string
	logdir        string
	join          string
	consistency   string
	durability    string
	parseSnapshot string
)

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <path>\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.BoolVarP(&version, "version", "V", false, "display version information")
	flag.BoolVarP(&debug, "debug", "D", false, "enable debug logging")

	flag.IntVar(&maxDatafileSize, "max-datafile-size", 1<<20, "maximum datafile size in bytes")

	flag.StringVarP(&bind, "bind", "b", "127.0.0.1:4920", "bind/discoverable ip:port")
	flag.StringVarP(&dir, "data", "d", "data", "data directory")
	flag.StringVarP(&logdir, "log-dir", "l", "", "log directory. If blank it will equals --data")
	flag.StringVarP(&join, "join", "j", "", "Join a cluster by providing an address")
	flag.StringVar(&consistency, "consistency", "high", "Consistency (low,medium,high)")
	flag.StringVar(&durability, "durability", "high", "Durability (low,medium,high)")
	flag.StringVar(&parseSnapshot, "parse-snapshot", "", "Parse and output a snapshot to Redis format")
}

func main() {
	flag.Parse()

	if debug {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	if version {
		fmt.Printf("bitraft version %s", FullVersion())
		os.Exit(0)
	}

	if parseSnapshot != "" {
		err := WriteRedisCommandsFromSnapshot(os.Stdout, parseSnapshot)
		if err != nil {
			log.Warningf("%v", err)
			os.Exit(1)
		}
		return
	}

	var lconsistency finn.Level
	switch strings.ToLower(consistency) {
	default:
		log.Warningf("invalid --consistency")
	case "low":
		lconsistency = finn.Low
	case "medium", "med":
		lconsistency = finn.Medium
	case "high":
		lconsistency = finn.High
	}

	var ldurability finn.Level
	switch strings.ToLower(durability) {
	default:
		log.Warningf("invalid --durability")
	case "low":
		ldurability = finn.Low
	case "medium", "med":
		ldurability = finn.Medium
	case "high":
		ldurability = finn.High
	}

	if logdir == "" {
		logdir = dir
	}

	if err := ListenAndServe(bind, join, dir, logdir, lconsistency, ldurability); err != nil {
		log.Warningf("%v", err)
	}
}
