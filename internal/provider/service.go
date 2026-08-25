package provider

import (
	"context"
	"sync"
	"time"
)

type Service struct {
	now func() time.Time

	configMu sync.RWMutex
	config   Config

	loginMu sync.Mutex
	logins  map[string]*loginSession

	modelMu    sync.Mutex
	modelCache map[string]cachedModels
}

type cachedModels struct {
	models    []CursorModel
	expiresAt time.Time
}

type loginSession struct {
	Verifier  string
	ExpiresAt time.Time
	Label     string
	Prefix    string
	Priority  int
}

func New() *Service {
	return &Service{
		now:        time.Now,
		config:     DefaultConfig(),
		logins:     make(map[string]*loginSession),
		modelCache: make(map[string]cachedModels),
	}
}

func (s *Service) Configure(raw []byte) error {
	cfg, err := ParseConfig(raw)
	if err != nil {
		return err
	}
	s.configMu.Lock()
	s.config = cfg
	s.configMu.Unlock()
	s.modelMu.Lock()
	s.modelCache = make(map[string]cachedModels)
	s.modelMu.Unlock()
	return nil
}

func (s *Service) Config() Config {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.config
}

func (s *Service) Shutdown() {
	s.loginMu.Lock()
	clear(s.logins)
	s.loginMu.Unlock()
	s.modelMu.Lock()
	clear(s.modelCache)
	s.modelMu.Unlock()
}

func contextWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, timeout)
}
