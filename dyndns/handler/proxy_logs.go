package handler

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var nginxLine = regexp.MustCompile(`^(\S+) \S+ \S+ \[([^]]+)\] "(\S+) (\S+) HTTP/[^\"]+" (\d{3}) \S+ "[^\"]*" "([^\"]*)"`)

func updatePath(p string) bool {
	return p == "/update" || p == "/nic/update" || p == "/v2/update" || p == "/v3/update"
}
func digest(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

func parseProxyLine(line string) *model.ProxyLog {
	m := nginxLine.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	u, err := url.ParseRequestURI(m[4])
	if err != nil || !updatePath(u.Path) {
		return nil
	}
	code, _ := strconv.Atoi(m[5])
	if code < 400 {
		return nil
	}
	when, err := time.Parse("02/Jan/2006:15:04:05 -0700", m[2])
	if err != nil {
		return nil
	}
	q := u.Query()
	name := strings.ToLower(strings.TrimSpace(q.Get("hostname")))
	if len(name) > 253 {
		name = name[:253]
	}
	ip := q.Get("myip")
	if net.ParseIP(ip) == nil {
		ip = ""
	}
	message := http.StatusText(code)
	if code == 499 {
		message = "Client closed request"
	}
	if message == "" {
		message = "Proxy request failed"
	}
	return &model.ProxyLog{Key: digest(line), TimeStamp: when, Hostname: name, CallerIP: m[1], SentIP: ip, UserAgent: m[6], Path: u.Path, Code: code, Message: message, Source: "nginx"}
}

// importProxyFile tails a bounded batch. The cursor and imported rows commit together.
// Inode identity survives renames; an anchor detects copy-truncate even after regrowth.
func (h *Handler) importProxyFile(path string) error {
	if strings.HasSuffix(path, ".gz") {
		return h.importCompressedProxyFile(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	st := info.Sys().(*syscall.Stat_t)
	key := fmt.Sprintf("%d:%d", st.Dev, st.Ino)
	var cursor model.LogCursor
	err = h.DB.First(&cursor, "key = ?", key).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	cursor.Key = key
	if cursor.Offset > info.Size() {
		cursor.Offset = 0
		cursor.Anchor = ""
	}
	if cursor.Offset > 0 && cursor.Anchor != "" {
		n := int64(128)
		if cursor.Offset < n {
			n = cursor.Offset
		}
		b := make([]byte, n)
		if _, err = f.ReadAt(b, cursor.Offset-n); err != nil || digest(string(b)) != cursor.Anchor {
			cursor.Offset = 0
			cursor.Anchor = ""
		}
	}
	if cursor.Offset == info.Size() {
		return nil
	}
	if _, err = f.Seek(cursor.Offset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(f)
	start := cursor.Offset
	rows := []model.ProxyLog{}
	cutoff := time.Time{}
	if h.ClearInterval > 0 {
		cutoff = time.Now().Add(-time.Duration(h.ClearInterval) * 24 * time.Hour)
	}
	for cursor.Offset-start < 4*1024*1024 {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			if readErr != io.EOF {
				return readErr
			}
			break
		} // leave partial lines for the next pass
		cursor.Offset += int64(len(line))
		if row := parseProxyLine(strings.TrimRight(line, "\r\n")); row != nil && !row.TimeStamp.Before(cutoff) {
			rows = append(rows, *row)
		}
	}
	n := int64(128)
	if cursor.Offset < n {
		n = cursor.Offset
	}
	b := make([]byte, n)
	if _, err = f.ReadAt(b, cursor.Offset-n); err != nil && err != io.EOF {
		return err
	}
	cursor.Anchor = digest(string(b))
	return h.DB.Transaction(func(tx *gorm.DB) error {
		if len(rows) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 100).Error; err != nil {
				return err
			}
		}
		return tx.Save(&cursor).Error
	})
}

// StartProxyLogs accepts only an explicitly mounted read-only directory. It never
// exposes the log files over HTTP. Rotated plain access.log* files are followed too.
func (h *Handler) StartProxyLogs() {
	dir := os.Getenv("DDNS_NGINX_LOG_DIR")
	if dir == "" {
		return
	}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			paths, err := filepath.Glob(filepath.Join(dir, "access.log*"))
			if err == nil && len(paths) == 0 {
				err = fmt.Errorf("no access.log files found")
			}
			for _, path := range paths {

				if e := h.importProxyFile(path); e != nil {
					err = e
					break
				}
			}
			h.proxyMu.Lock()
			h.proxyError = ""
			if err != nil {
				h.proxyError = "Proxy log import unavailable; check mount and read permissions."
			}
			h.proxyMu.Unlock()
			if h.ClearInterval > 0 {
				h.DB.Where("time_stamp < ?", time.Now().Add(-time.Duration(h.ClearInterval)*24*time.Hour)).Delete(&model.ProxyLog{})
			}
			<-ticker.C
		}
	}()
}

// Compressed rotations are immutable snapshots. Read each once; the raw-line
// digest deduplicates a rotation that was already consumed as a plain file.
func (h *Handler) importCompressedProxyFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("gzip:%s:%d:%d", filepath.Base(path), info.Size(), info.ModTime().UnixNano())
	var done int64
	if err = h.DB.Model(&model.LogCursor{}).Where("key = ?", key).Count(&done).Error; err != nil {
		return err
	}
	if done > 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	reader, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer reader.Close()
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	cutoff := time.Time{}
	if h.ClearInterval > 0 {
		cutoff = time.Now().Add(-time.Duration(h.ClearInterval) * 24 * time.Hour)
	}
	batch := []model.ProxyLog{}
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := h.DB.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(batch, 100).Error
		batch = nil
		return err
	}
	for scan.Scan() {
		if row := parseProxyLine(scan.Text()); row != nil && !row.TimeStamp.Before(cutoff) {
			batch = append(batch, *row)
		}
		if len(batch) >= 100 {
			if err = flush(); err != nil {
				return err
			}
		}
	}
	if err = scan.Err(); err != nil {
		return err
	}
	if err = flush(); err != nil {
		return err
	}
	return h.DB.Create(&model.LogCursor{Key: key, Offset: info.Size()}).Error
}
