package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/clientcatalog"
)

const (
	clientDownloadMaxBytes   = int64(512 << 20)
	clientDownloadCacheBytes = int64(1 << 30)
	clientDownloadTTL        = 24 * time.Hour
	clientDownloadTimeout    = 10 * time.Minute
)

type clientDownloadError struct {
	status  int
	message string
}

func (e *clientDownloadError) Error() string         { return e.message }
func downloadError(status int, message string) error { return &clientDownloadError{status, message} }

// A cache entry is shared by all readers. Bytes are published only after the
// corresponding write succeeds; no installer is held in the Go heap.
type clientDownloadEntry struct {
	url, filename, file, etag  string
	ready, changed             chan struct{}
	readySent, done, abandoned bool
	size, expected             int64
	refs                       int
	err                        error
	used, created              time.Time
	cancel                     context.CancelFunc
}

type clientDownloadRelay struct {
	mu                       sync.Mutex
	entries                  map[string]*clientDownloadEntry
	client                   *http.Client
	dir                      string
	maxBytes, cacheBytes     int64
	ttl, timeout             time.Duration
	fetchSlots, requestSlots chan struct{}
	wg                       sync.WaitGroup
	closed                   bool
}

func newClientDownloadRelay() *clientDownloadRelay {
	transport := &http.Transport{
		// No environment proxy, browser cookies or authorization is forwarded.
		DialContext: publicSourceDial, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second, IdleConnTimeout: 30 * time.Second,
		MaxIdleConns: 4, MaxIdleConnsPerHost: 2, DisableCompression: true,
	}
	return &clientDownloadRelay{
		entries:  make(map[string]*clientDownloadEntry),
		client:   &http.Client{Transport: transport, CheckRedirect: clientDownloadRedirect},
		maxBytes: clientDownloadMaxBytes, cacheBytes: clientDownloadCacheBytes,
		ttl: clientDownloadTTL, timeout: clientDownloadTimeout,
		fetchSlots: make(chan struct{}, 2), requestSlots: make(chan struct{}, 8),
	}
}

// The initial URL must be a fixed, reviewed GitHub release asset, never a user
// supplied URL, release HTML page, branch file, app store or another mirror.
func clientDownloadURL(raw string, initial bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Hostname() == "" || (u.Port() != "" && u.Port() != "443") {
		return nil, errors.New("untrusted client download URL")
	}
	host := strings.ToLower(u.Hostname())
	if host == "github.com" {
		parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		if len(parts) != 6 || parts[0] == "" || parts[1] == "" || parts[2] != "releases" || parts[3] != "download" || parts[4] == "" || parts[5] == "" || u.RawQuery != "" {
			return nil, errors.New("client download must be a fixed release asset")
		}
		for _, part := range parts {
			if part == "." || part == ".." || strings.ContainsAny(part, "\\\r\n\x00") {
				return nil, errors.New("invalid asset path")
			}
		}
		if !clientInstallerName(parts[5]) {
			return nil, errors.New("unsupported installer file")
		}
	} else if initial || (host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com" && host != "github-releases.githubusercontent.com") {
		return nil, errors.New("untrusted asset host")
	}
	return u, nil
}

func clientInstallerName(name string) bool {
	if len(name) > 240 || strings.ContainsAny(name, "/\\\r\n\x00") {
		return false
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".exe", ".dmg", ".deb", ".zip", ".apk", ".msi", ".rpm", ".appimage":
		return true
	}
	return false
}

func clientDownloadRedirect(r *http.Request, via []*http.Request) error {
	if len(via) > 4 {
		return errors.New("too many client asset redirects")
	}
	if _, err := clientDownloadURL(r.URL.String(), false); err != nil {
		return err
	}
	for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Referer", "Range", "If-Range"} {
		r.Header.Del(name)
	}
	return nil
}

func clientInstallerType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".apk":
		return "application/vnd.android.package-archive"
	case ".dmg":
		return "application/x-apple-diskimage"
	case ".deb":
		return "application/vnd.debian.binary-package"
	case ".zip":
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}

func (a *App) clientDownload(w http.ResponseWriter, r *http.Request, actor Record) {
	if actor.ID <= 0 || !actor.Enabled {
		failure(w, 401, "请登录后下载客户端")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		failure(w, 405, "方法不支持")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/clients/download/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/api/clients/download/") || len(parts) != 2 || r.URL.RawQuery != "" {
		failure(w, 404, "客户端安装包不存在")
		return
	}
	index, err := strconv.Atoi(parts[1])
	item, ok := clientcatalog.Lookup(parts[0], index)
	if err != nil || strconv.Itoa(index) != parts[1] || !ok {
		failure(w, 404, "客户端安装包不存在")
		return
	}
	u, err := clientDownloadURL(item.URL, true)
	if err != nil {
		failure(w, 404, "此客户端请从官方商店下载")
		return
	}
	if !a.store.siteSettings().ClientDownloadRelay {
		failure(w, 503, "本站暂未开启客户端下载中继，请使用官方下载")
		return
	}
	a.clientDownloadMu.Lock()
	if a.clientDownloads == nil {
		a.clientDownloads = newClientDownloadRelay()
	}
	relay := a.clientDownloads
	a.clientDownloadMu.Unlock()
	relay.serve(w, r, item.URL, path.Base(u.Path))
}

func (a *App) closeClientDownloads() {
	a.clientDownloadMu.Lock()
	relay := a.clientDownloads
	a.clientDownloadMu.Unlock()
	if relay != nil {
		relay.close()
	}
}

func (d *clientDownloadRelay) close() {
	d.mu.Lock()
	d.closed = true
	for _, e := range d.entries {
		e.cancel()
	}
	d.mu.Unlock()
	d.wg.Wait()
	d.client.CloseIdleConnections()
	d.mu.Lock()
	dir := d.dir
	d.mu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

func (d *clientDownloadRelay) notifyLocked(e *clientDownloadEntry) {
	close(e.changed)
	e.changed = make(chan struct{})
}

// Reserve the maximum file size for each in-flight fetch. Evict only idle
// completed entries; active readers are never deleted to make room.
func (d *clientDownloadRelay) makeRoomLocked() bool {
	for {
		var used int64
		var oldest *clientDownloadEntry
		for _, e := range d.entries {
			if e.done {
				used += e.size
			} else {
				used += d.maxBytes
			}
			if e.done && e.refs == 0 && (oldest == nil || e.used.Before(oldest.used)) {
				oldest = e
			}
		}
		if used+d.maxBytes <= d.cacheBytes {
			return true
		}
		if oldest == nil {
			return false
		}
		delete(d.entries, oldest.url)
		_ = os.Remove(oldest.file)
	}
}

func (d *clientDownloadRelay) acquire(raw, filename string, head bool) (*clientDownloadEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, downloadError(503, "下载服务正在重启，请稍后重试")
	}
	for key, e := range d.entries {
		if e.done && e.refs == 0 && time.Since(e.created) > d.ttl {
			delete(d.entries, key)
			_ = os.Remove(e.file)
		}
	}
	if e := d.entries[raw]; e != nil {
		if e.abandoned {
			return nil, downloadError(503, "上次下载正在取消，请稍后重试")
		}
		e.refs++
		e.used = time.Now()
		return e, nil
	}
	// A HEAD request must not spend bandwidth downloading a missing installer.
	if head {
		return nil, nil
	}
	select {
	case d.fetchSlots <- struct{}{}:
	default:
		return nil, downloadError(503, "下载中继繁忙，请稍后重试或使用官方下载")
	}
	if !d.makeRoomLocked() {
		<-d.fetchSlots
		return nil, downloadError(503, "下载缓存繁忙，请稍后重试")
	}
	if d.dir == "" {
		var err error
		d.dir, err = os.MkdirTemp("", "guangyue-client-downloads-")
		if err != nil {
			<-d.fetchSlots
			return nil, downloadError(503, "下载缓存暂不可用，请使用官方下载")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	e := &clientDownloadEntry{url: raw, filename: filename, ready: make(chan struct{}), changed: make(chan struct{}), expected: -1, refs: 1, used: time.Now(), created: time.Now(), cancel: cancel}
	d.entries[raw] = e
	d.wg.Add(1)
	go d.fetch(ctx, e)
	return e, nil
}

func (d *clientDownloadRelay) release(e *clientDownloadEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e.refs--
	e.used = time.Now()
	if e.refs == 0 && !e.done {
		e.abandoned = true
		e.cancel()
	} else if e.refs == 0 && (e.err != nil || e.abandoned) {
		d.removeLocked(e)
	}
}

func (d *clientDownloadRelay) removeLocked(e *clientDownloadEntry) {
	if d.entries[e.url] == e {
		delete(d.entries, e.url)
	}
	_ = os.Remove(e.file)
}

func (d *clientDownloadRelay) fetch(ctx context.Context, e *clientDownloadEntry) {
	defer d.wg.Done()
	defer func() { <-d.fetchSlots }()
	defer e.cancel()
	err := d.fetchFile(ctx, e)
	d.mu.Lock()
	e.err = err
	e.done = true
	if !e.readySent {
		e.readySent = true
		close(e.ready)
	}
	d.notifyLocked(e)
	// Keep failed files charged to the cache budget until the last reader
	// closes its descriptor. Unlinking an open file would hide its disk usage.
	if (err != nil || e.abandoned) && e.refs == 0 {
		d.removeLocked(e)
	}
	d.mu.Unlock()
}

func (d *clientDownloadRelay) fetchFile(ctx context.Context, e *clientDownloadEntry) error {
	if _, err := clientDownloadURL(e.url, true); err != nil {
		return downloadError(502, "安装包来源无效，请使用官方下载")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", e.url, nil)
	if err != nil {
		return downloadError(502, "安装包地址无效")
	}
	req.Header.Set("User-Agent", "Guangyue-Client-Download/"+version)
	req.Header.Set("Accept", "application/octet-stream")
	response, err := d.client.Do(req)
	if err != nil {
		return downloadError(502, "无法连接官方安装包，请稍后重试或使用官方下载")
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return downloadError(404, "官方安装包已不存在，请打开官方发布页")
	}
	if response.StatusCode != http.StatusOK {
		return downloadError(502, "官方安装包暂不可用，请稍后重试")
	}
	if response.ContentLength > d.maxBytes {
		return downloadError(502, "安装包超过本站 512 MiB 中继上限，请使用官方下载")
	}
	if response.ContentLength == 0 {
		return downloadError(502, "官方安装包为空，请使用官方下载")
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/") || strings.Contains(contentType, "json") || strings.Contains(contentType, "html") {
		return downloadError(502, "官方返回的内容不是安装包，请使用官方下载")
	}
	file, err := os.CreateTemp(d.dir, "asset-")
	if err != nil {
		return downloadError(503, "下载缓存暂不可用，请使用官方下载")
	}
	defer file.Close()
	d.mu.Lock()
	e.file = file.Name()
	e.expected = response.ContentLength
	e.readySent = true
	close(e.ready)
	d.mu.Unlock()
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if total+int64(n) > d.maxBytes {
				return downloadError(502, "安装包超过本站中继上限")
			}
			if _, err = file.Write(buffer[:n]); err != nil {
				return downloadError(503, "下载缓存写入失败")
			}
			_, _ = hash.Write(buffer[:n])
			total += int64(n)
			d.mu.Lock()
			e.size = total
			d.notifyLocked(e)
			d.mu.Unlock()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return downloadError(502, "官方安装包下载中断，请重新下载")
		}
		if ctx.Err() != nil {
			return downloadError(502, "客户端下载超时，请重试或使用官方下载")
		}
	}
	if total == 0 || (response.ContentLength >= 0 && total != response.ContentLength) {
		return downloadError(502, "安装包不完整，请重新下载")
	}
	if err = file.Close(); err != nil {
		return downloadError(503, "下载缓存写入失败")
	}
	d.mu.Lock()
	e.etag = "\"" + hex.EncodeToString(hash.Sum(nil)) + "\""
	d.mu.Unlock()
	return nil
}

func clientDownloadFailure(w http.ResponseWriter, err error) {
	var value *clientDownloadError
	if errors.As(err, &value) {
		if value.status == 503 {
			w.Header().Set("Retry-After", "5")
		}
		failure(w, value.status, value.message)
		return
	}
	failure(w, 502, "客户端下载失败，请重试或使用官方下载")
}

func (d *clientDownloadRelay) serve(w http.ResponseWriter, r *http.Request, raw, filename string) {
	select {
	case d.requestSlots <- struct{}{}:
		defer func() { <-d.requestSlots }()
	default:
		clientDownloadFailure(w, downloadError(503, "当前下载人数较多，请稍后重试"))
		return
	}
	entry, err := d.acquire(raw, filename, r.Method == "HEAD")
	if err != nil {
		clientDownloadFailure(w, err)
		return
	}
	// Override only this long transfer, leaving the normal API timeout intact.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(20 * time.Minute))
	if entry != nil {
		defer d.release(entry)
		select {
		case <-entry.ready:
		case <-r.Context().Done():
			return
		}
		d.mu.Lock()
		err = entry.err
		d.mu.Unlock()
		if err != nil {
			clientDownloadFailure(w, err)
			return
		}
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Content-Type", clientInstallerType(filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	if entry == nil {
		w.Header().Set("Accept-Ranges", "none")
		w.WriteHeader(http.StatusOK)
		return
	}
	d.mu.Lock()
	complete, expected, filePath, etag, created, entryErr := entry.done, entry.expected, entry.file, entry.etag, entry.created, entry.err
	d.mu.Unlock()
	// The fetch may fail after the initial ready signal but before this snapshot.
	// Never pass a failed completed file to ServeContent: doing so can emit a
	// shorter 200 response and hide the upstream truncation from the client.
	if entryErr != nil {
		clientDownloadFailure(w, entryErr)
		return
	}
	file, err := os.Open(filePath)
	if err != nil {
		w.Header().Del("Content-Disposition")
		clientDownloadFailure(w, downloadError(503, "下载缓存已失效，请重新下载"))
		return
	}
	defer file.Close()
	if complete {
		w.Header().Set("ETag", etag)
		http.ServeContent(w, r, filename, created, file)
		return
	}
	// Cold or in-progress Range requests receive a full 200 response. After the
	// cached file completes, ServeContent supports byte ranges and If-Range.
	w.Header().Set("Accept-Ranges", "none")
	if expected >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(expected, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == "HEAD" {
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.Flush()
	buffer := make([]byte, 64<<10)
	var offset int64
	for {
		d.mu.Lock()
		size, done, readErr, changed := entry.size, entry.done, entry.err, entry.changed
		d.mu.Unlock()
		if done && readErr != nil {
			panic(http.ErrAbortHandler)
		}
		if size > offset {
			amount := int64(len(buffer))
			if size-offset < amount {
				amount = size - offset
			}
			n, err := file.ReadAt(buffer[:amount], offset)
			if n > 0 {
				if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
					return
				}
				offset += int64(n)
				_ = controller.Flush()
			}
			if err != nil && err != io.EOF {
				panic(http.ErrAbortHandler)
			}
			continue
		}
		if done {
			return
		}
		select {
		case <-changed:
		case <-r.Context().Done():
			return
		}
	}
}

// Keep a compile-time assertion that sanitized failures remain ordinary errors.
var _ error = (*clientDownloadError)(nil)
