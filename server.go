package main

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prologic/bitcask"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/finn"
	"github.com/tidwall/redcon"
)

const defaultTCPKeepAlive = time.Minute * 5

var (
	errSyntaxError = errors.New("syntax error")
)

func ListenAndServe(addr, join, dir, logdir string, consistency, durability finn.Level) error {
	opts := finn.Options{
		Backend:     finn.FastLog,
		Consistency: consistency,
		Durability:  durability,
		ConnAccept: func(conn redcon.Conn) bool {
			if tcp, ok := conn.NetConn().(*net.TCPConn); ok {
				if err := tcp.SetKeepAlive(true); err != nil {
					log.Warningf("could not set keepalive: %s",
						tcp.RemoteAddr().String())
				} else {
					err := tcp.SetKeepAlivePeriod(defaultTCPKeepAlive)
					if err != nil {
						log.Warningf("could not set keepalive period: %s",
							tcp.RemoteAddr().String())
					}
				}
			}
			return true
		},
	}
	m, err := NewMachine(dir, addr)
	if err != nil {
		return err
	}
	n, err := finn.Open(logdir, addr, join, m, &opts)
	if err != nil {
		return err
	}
	defer n.Close()

	select {
	// blocking, there's no way out
	}
}

type Machine struct {
	mu     sync.RWMutex
	dir    string
	db     *bitcask.Bitcask
	dbPath string
	addr   string
	closed bool
}

func NewMachine(dir, addr string) (*Machine, error) {
	kvm := &Machine{
		dir:  dir,
		addr: addr,
	}
	var err error
	kvm.dbPath = filepath.Join(dir, "node.db")
	kvm.db, err = bitcask.Open(kvm.dir)
	if err != nil {
		return nil, err
	}
	return kvm, nil
}

func (kvm *Machine) Close() error {
	kvm.mu.Lock()
	defer kvm.mu.Unlock()
	kvm.db.Close()
	kvm.closed = true
	return nil
}

func (kvm *Machine) Command(
	m finn.Applier, conn redcon.Conn, cmd redcon.Command,
) (interface{}, error) {
	switch strings.ToLower(string(cmd.Args[0])) {
	default:
		log.Warningf("unknown command: %s\n", cmd.Args[0])
		return nil, finn.ErrUnknownCommand
	case "echo":
		return kvm.cmdEcho(m, conn, cmd)
	case "set":
		return kvm.cmdSet(m, conn, cmd)
	case "get":
		return kvm.cmdGet(m, conn, cmd)
	case "del":
		return kvm.cmdDel(m, conn, cmd)
	case "keys":
		return kvm.cmdKeys(m, conn, cmd)
	case "flushdb":
		return kvm.cmdFlushdb(m, conn, cmd)
	case "shutdown":
		log.Warningf("shutting down")
		conn.WriteString("OK")
		conn.Close()
		os.Exit(0)
		return nil, nil
	}
}

func (kvm *Machine) Restore(rd io.Reader) error {
	kvm.mu.Lock()
	defer kvm.mu.Unlock()
	var err error
	if err := kvm.db.Close(); err != nil {
		return err
	}
	if err := os.RemoveAll(kvm.dbPath); err != nil {
		return err
	}
	kvm.db = nil
	kvm.db, err = bitcask.Open(kvm.dir)
	if err != nil {
		return err
	}
	num := make([]byte, 8)
	gzr, err := gzip.NewReader(rd)
	if err != nil {
		return err
	}
	r := bufio.NewReader(gzr)
	for {
		if _, err := io.ReadFull(r, num); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		key := make([]byte, int(binary.LittleEndian.Uint64(num)))
		if _, err := io.ReadFull(r, key); err != nil {
			return err
		}
		if _, err := io.ReadFull(r, num); err != nil {
			return err
		}
		value := make([]byte, int(binary.LittleEndian.Uint64(num)))
		if _, err := io.ReadFull(r, value); err != nil {
			return err
		}
		kvm.db.Put(string(key), value)
	}
	return gzr.Close()
}

// WriteRedisCommandsFromSnapshot will read a snapshot and write all the
// Redis SET commands needed to rebuild the entire database.
// The commands are written to wr.
func WriteRedisCommandsFromSnapshot(wr io.Writer, snapshotPath string) error {
	f, err := os.Open(snapshotPath)
	if err != nil {
		return err
	}
	defer f.Close()
	var cmd []byte
	num := make([]byte, 8)
	var gzclosed bool
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() {
		if !gzclosed {
			gzr.Close()
		}
	}()
	r := bufio.NewReader(gzr)
	for {
		if _, err := io.ReadFull(r, num); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		key := make([]byte, int(binary.LittleEndian.Uint64(num)))
		if _, err := io.ReadFull(r, key); err != nil {
			return err
		}
		if _, err := io.ReadFull(r, num); err != nil {
			return err
		}
		value := make([]byte, int(binary.LittleEndian.Uint64(num)))
		if _, err := io.ReadFull(r, value); err != nil {
			return err
		}
		if len(key) == 0 || key[0] != 'k' {
			// do not accept keys that do not start with 'k'
			continue
		}
		key = key[1:]
		cmd = cmd[:0]
		cmd = append(cmd, "*3\r\n$3\r\nSET\r\n$"...)
		cmd = strconv.AppendInt(cmd, int64(len(key)), 10)
		cmd = append(cmd, '\r', '\n')
		cmd = append(cmd, key...)
		cmd = append(cmd, '\r', '\n', '$')
		cmd = strconv.AppendInt(cmd, int64(len(value)), 10)
		cmd = append(cmd, '\r', '\n')
		cmd = append(cmd, value...)
		cmd = append(cmd, '\r', '\n')
		if _, err := wr.Write(cmd); err != nil {
			return err
		}
	}
	err = gzr.Close()
	gzclosed = true
	return err
}

func (kvm *Machine) Snapshot(wr io.Writer) error {
	kvm.mu.RLock()
	defer kvm.mu.RUnlock()
	gzw := gzip.NewWriter(wr)

	err := kvm.db.Fold(func(key string) error {
		var buf []byte
		value, err := kvm.db.Get(key)
		if err != nil {
			return err
		}

		num := make([]byte, 8)
		binary.LittleEndian.PutUint64(num, uint64(len(key)))
		buf = append(buf, num...)
		buf = append(buf, key...)
		binary.LittleEndian.PutUint64(num, uint64(len(value)))
		buf = append(buf, num...)
		buf = append(buf, value...)
		if _, err := gzw.Write(buf); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	return gzw.Close()
}

func (kvm *Machine) cmdSet(
	m finn.Applier, conn redcon.Conn, cmd redcon.Command,
) (interface{}, error) {
	if len(cmd.Args) != 3 {
		return nil, finn.ErrWrongNumberOfArguments
	}
	return m.Apply(conn, cmd,
		func() (interface{}, error) {
			kvm.mu.Lock()
			defer kvm.mu.Unlock()
			return nil, kvm.db.Put(string(cmd.Args[1]), cmd.Args[2])
		},
		func(v interface{}) (interface{}, error) {
			conn.WriteString("OK")
			return nil, nil
		},
	)
}

func (kvm *Machine) cmdEcho(m finn.Applier, conn redcon.Conn, cmd redcon.Command) (interface{}, error) {
	if len(cmd.Args) != 2 {
		return nil, finn.ErrWrongNumberOfArguments
	}
	conn.WriteBulk(cmd.Args[1])
	return nil, nil
}

func (kvm *Machine) cmdGet(m finn.Applier, conn redcon.Conn, cmd redcon.Command) (interface{}, error) {
	if len(cmd.Args) != 2 {
		return nil, finn.ErrWrongNumberOfArguments
	}
	key := string(cmd.Args[1])
	return m.Apply(conn, cmd, nil,
		func(interface{}) (interface{}, error) {
			kvm.mu.RLock()
			defer kvm.mu.RUnlock()
			value, err := kvm.db.Get(key)
			if err != nil {
				if strings.HasPrefix(err.Error(), "error: key not found") {
					conn.WriteNull()
					return nil, nil
				}
				return nil, err
			}
			conn.WriteBulk(value)
			return nil, nil
		},
	)
}

func (kvm *Machine) cmdDel(m finn.Applier, conn redcon.Conn, cmd redcon.Command) (interface{}, error) {
	var startIdx = 1
	return m.Apply(conn, cmd,
		func() (interface{}, error) {
			kvm.mu.Lock()
			defer kvm.mu.Unlock()
			var n int
			for i := startIdx; i < len(cmd.Args); i++ {
				key := string(cmd.Args[i])
				err := kvm.db.Delete(key)
				if err != nil {
					return 0, err
				}
				n++
			}
			return n, nil
		},
		func(v interface{}) (interface{}, error) {
			n := v.(int)
			conn.WriteInt(n)
			return nil, nil
		},
	)
}

func (kvm *Machine) cmdKeys(m finn.Applier, conn redcon.Conn, cmd redcon.Command) (interface{}, error) {
	if len(cmd.Args) < 2 {
		return nil, finn.ErrWrongNumberOfArguments
	}
	var withvalues bool
	for i := 2; i < len(cmd.Args); i++ {
		switch strings.ToLower(string(cmd.Args[i])) {
		default:
			return nil, errSyntaxError
		case "withvalues":
			withvalues = true
		}
	}
	return m.Apply(conn, cmd, nil,
		func(interface{}) (interface{}, error) {
			kvm.mu.RLock()
			defer kvm.mu.RUnlock()
			var keys [][]byte
			var values [][]byte

			err := kvm.db.Fold(func(key string) error {
				keys = append(keys, []byte(key))
				if withvalues {
					value, err := kvm.db.Get(key)
					if err != nil {
						return err
					}
					values = append(values, value)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			if withvalues {
				conn.WriteArray(len(keys) * 2)
			} else {
				conn.WriteArray(len(keys))
			}
			for i := 0; i < len(keys); i++ {
				conn.WriteBulk(keys[i])
				if withvalues {
					conn.WriteBulk(values[i])
				}
			}
			return nil, nil
		},
	)
}

func (kvm *Machine) cmdFlushdb(m finn.Applier, conn redcon.Conn, cmd redcon.Command) (interface{}, error) {
	if len(cmd.Args) != 1 {
		return nil, finn.ErrWrongNumberOfArguments
	}
	return m.Apply(conn, cmd,
		func() (interface{}, error) {
			kvm.mu.Lock()
			defer kvm.mu.Unlock()
			if err := kvm.db.Sync(); err != nil {
				panic(err.Error())
			}
			return nil, nil
		},
		func(v interface{}) (interface{}, error) {
			conn.WriteString("OK")
			return nil, nil
		},
	)
}
