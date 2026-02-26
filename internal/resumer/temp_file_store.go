package resumer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chirino/memory-service/internal/tempfiles"
)

const (
	tempFilePrefix          = "response-resume-"
	tempFileSuffix          = ".tokens"
	defaultLocatorTTL       = 10 * time.Second
	defaultLocatorRefresh   = 5 * time.Second
	defaultReplayBufferSize = 64
)

// Store is the response recorder backend implementation backed by local temp files.
type Store struct {
	tempDir        string
	retention      time.Duration
	locatorStore   LocatorStore
	locatorTTL     time.Duration
	locatorRefresh time.Duration

	mu         sync.RWMutex
	recordings map[string]*recording
}

type recording struct {
	conversationID string
	path           string
	file           *os.File
	cancelCh       chan struct{}

	mu          sync.Mutex
	size        int64
	complete    bool
	completedAt time.Time

	refreshStop sync.Once
	stopRefresh chan struct{}
}

// Recorder writes response tokens to the active recording.
type Recorder struct {
	store  *Store
	convID string
	rec    *recording
	once   sync.Once
}

func NewTempFileStore(tempDir string, retention time.Duration, locatorStore LocatorStore) *Store {
	if strings.TrimSpace(tempDir) == "" {
		tempDir = os.TempDir()
	}
	if retention <= 0 {
		retention = 30 * time.Minute
	}
	if locatorStore == nil {
		locatorStore = noopLocatorStore{}
	}

	s := &Store{
		tempDir:        tempDir,
		retention:      retention,
		locatorStore:   locatorStore,
		locatorTTL:     defaultLocatorTTL,
		locatorRefresh: defaultLocatorRefresh,
		recordings:     map[string]*recording{},
	}
	s.cleanupStaleTempFiles()
	return s
}

func (s *Store) HasResponseInProgress(_ context.Context, conversationID string) (bool, error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupExpiredLocked(now)

	rec, ok := s.recordings[conversationID]
	if !ok {
		return false, nil
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return !rec.complete, nil
}

func (s *Store) Check(ctx context.Context, conversationIDs []string) ([]string, error) {
	active := make([]string, 0, len(conversationIDs))
	for _, id := range conversationIDs {
		inProgress, err := s.HasResponseInProgress(ctx, id)
		if err != nil {
			return nil, err
		}
		if inProgress {
			active = append(active, id)
		}
	}
	return active, nil
}

func (s *Store) RecorderWithAddress(ctx context.Context, conversationID string, advertisedAddress string) (*Recorder, error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupExpiredLocked(now)

	if existing, ok := s.recordings[conversationID]; ok {
		s.completeLocked(ctx, conversationID, existing)
	}

	file, err := tempfiles.Create(s.tempDir, tempFilePrefix+"*"+tempFileSuffix)
	if err != nil {
		return nil, err
	}

	rec := &recording{
		conversationID: conversationID,
		path:           file.Name(),
		file:           file,
		cancelCh:       make(chan struct{}),
		stopRefresh:    make(chan struct{}),
	}
	s.recordings[conversationID] = rec

	locator := locatorFromAddress(advertisedAddress, filepath.Base(file.Name()))
	if s.locatorStore.Available() {
		if err := s.locatorStore.Upsert(ctx, conversationID, locator, s.locatorTTL); err != nil {
			return nil, err
		}
		s.startLocatorRefresh(conversationID, rec, locator)
	}

	return &Recorder{store: s, convID: conversationID, rec: rec}, nil
}

func (s *Store) ReplayWithAddress(ctx context.Context, conversationID string, advertisedAddress string) (<-chan string, string, error) {
	if s.locatorStore.Available() && strings.TrimSpace(advertisedAddress) != "" {
		locator, err := s.locatorStore.Get(ctx, conversationID)
		if err != nil {
			return nil, "", err
		}
		if locator != nil && !locator.MatchesAddress(advertisedAddress) {
			return nil, locator.Address(), nil
		}
	}

	now := time.Now()
	s.mu.Lock()
	s.cleanupExpiredLocked(now)
	rec, ok := s.recordings[conversationID]
	s.mu.Unlock()
	if !ok {
		ch := make(chan string)
		close(ch)
		return ch, "", nil
	}

	ch := make(chan string, defaultReplayBufferSize)
	go s.replayFromFile(ctx, rec, ch)
	return ch, "", nil
}

func (s *Store) RequestCancelWithAddress(ctx context.Context, conversationID string, advertisedAddress string) (string, error) {
	if s.locatorStore.Available() && strings.TrimSpace(advertisedAddress) != "" {
		locator, err := s.locatorStore.Get(ctx, conversationID)
		if err != nil {
			return "", err
		}
		if locator != nil && !locator.MatchesAddress(advertisedAddress) {
			return locator.Address(), nil
		}
	}

	s.mu.RLock()
	rec := s.recordings[conversationID]
	s.mu.RUnlock()
	if rec == nil {
		return "", nil
	}
	select {
	case <-rec.cancelCh:
	default:
		close(rec.cancelCh)
	}
	return "", nil
}

func (s *Store) CancelStream(_ context.Context, conversationID string) (<-chan struct{}, error) {
	s.mu.RLock()
	rec := s.recordings[conversationID]
	s.mu.RUnlock()
	if rec == nil {
		ch := make(chan struct{})
		close(ch)
		return ch, nil
	}
	return rec.cancelCh, nil
}

func (r *Recorder) Record(token string) error {
	if token == "" {
		return nil
	}

	r.rec.mu.Lock()
	defer r.rec.mu.Unlock()

	if r.rec.complete || r.rec.file == nil {
		return nil
	}

	n, err := io.WriteString(r.rec.file, token)
	if err != nil {
		return err
	}
	r.rec.size += int64(n)
	return nil
}

func (r *Recorder) Complete() error {
	var result error
	r.once.Do(func() {
		result = r.store.complete(r.convID, r.rec)
	})
	return result
}

func (s *Store) complete(conversationID string, rec *recording) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completeLocked(context.Background(), conversationID, rec)
}

func (s *Store) completeLocked(ctx context.Context, conversationID string, rec *recording) error {
	rec.mu.Lock()
	if rec.complete {
		rec.mu.Unlock()
		return nil
	}
	rec.complete = true
	rec.completedAt = time.Now()
	file := rec.file
	rec.file = nil
	rec.mu.Unlock()

	rec.refreshStop.Do(func() {
		close(rec.stopRefresh)
	})

	if s.locatorStore.Available() {
		if err := s.locatorStore.Remove(ctx, conversationID); err != nil {
			return err
		}
	}

	if file != nil {
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) startLocatorRefresh(conversationID string, rec *recording, locator Locator) {
	if s.locatorRefresh <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(s.locatorRefresh)
		defer ticker.Stop()
		for {
			select {
			case <-rec.stopRefresh:
				return
			case <-ticker.C:
				rec.mu.Lock()
				done := rec.complete
				rec.mu.Unlock()
				if done {
					return
				}
				_ = s.locatorStore.Upsert(context.Background(), conversationID, locator, s.locatorTTL)
			}
		}
	}()
}

func (s *Store) replayFromFile(ctx context.Context, rec *recording, out chan<- string) {
	defer close(out)

	reader, err := os.Open(rec.path)
	if err != nil {
		return
	}
	defer reader.Close()

	offset := int64(0)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		rec.mu.Lock()
		size := rec.size
		done := rec.complete
		rec.mu.Unlock()

		if size > offset {
			chunk, readErr := readRange(reader, offset, size-offset)
			if readErr != nil {
				return
			}
			offset = size
			if len(chunk) > 0 {
				select {
				case <-ctx.Done():
					return
				case out <- string(chunk):
				}
			}
			continue
		}

		if done {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *Store) cleanupExpiredLocked(now time.Time) {
	if s.retention <= 0 {
		return
	}
	for conversationID, rec := range s.recordings {
		rec.mu.Lock()
		done := rec.complete
		completedAt := rec.completedAt
		path := rec.path
		rec.mu.Unlock()

		if !done {
			continue
		}
		if now.Sub(completedAt) < s.retention {
			continue
		}
		delete(s.recordings, conversationID)
		if path != "" {
			_ = os.Remove(path)
		}
	}
}

func (s *Store) cleanupStaleTempFiles() {
	if s.retention <= 0 {
		return
	}
	if err := os.MkdirAll(s.tempDir, 0o700); err != nil {
		return
	}
	entries, err := os.ReadDir(s.tempDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-s.retention)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, tempFilePrefix) || !strings.HasSuffix(name, tempFileSuffix) {
			continue
		}
		path := filepath.Join(s.tempDir, name)
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
	}
}

func readRange(file *os.File, offset int64, length int64) ([]byte, error) {
	if length <= 0 {
		return nil, nil
	}
	if length > 1<<20 {
		length = 1 << 20
	}
	buf := make([]byte, length)
	n, err := file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read response recording: %w", err)
	}
	if n <= 0 {
		return nil, nil
	}
	return buf[:n], nil
}
