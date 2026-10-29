package secret

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"
)

var ErrNotFound = errors.New("secret not found")

var safeFileName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type Value struct {
	plaintext string
}

func NewValue(plaintext string) Value {
	return Value{plaintext: plaintext}
}

func (v Value) Plaintext() string {
	return v.plaintext
}

func (v Value) String() string {
	return "[REDACTED]"
}

type Provider interface {
	Get(ctx context.Context, name string) (Value, error)
}

type MemoryProvider struct {
	values map[string]string
}

func NewMemoryProvider(values map[string]string) *MemoryProvider {
	copyValues := make(map[string]string, len(values))
	for key, value := range values {
		copyValues[key] = value
	}
	return &MemoryProvider{values: copyValues}
}

func (p *MemoryProvider) Get(ctx context.Context, name string) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	value, ok := p.values[name]
	if !ok {
		return Value{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return NewValue(value), nil
}

type EnvProvider struct {
	prefix string
}

func NewEnvProvider(prefix string) *EnvProvider {
	return &EnvProvider{prefix: prefix}
}

func (p *EnvProvider) Get(ctx context.Context, name string) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	key := p.prefix + envSecretName(name)
	value, ok := os.LookupEnv(key)
	if !ok {
		return Value{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return NewValue(value), nil
}

type FileProvider struct {
	root string
}

func NewFileProvider(root string) *FileProvider {
	return &FileProvider{root: root}
}

func (p *FileProvider) Get(ctx context.Context, name string) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !safeFileName.MatchString(name) {
		return Value{}, fmt.Errorf("invalid secret file name %q", name)
	}
	path := filepath.Join(p.root, name)
	root, err := filepath.Abs(p.root)
	if err != nil {
		return Value{}, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return Value{}, err
	}
	if absPath != root && !strings.HasPrefix(absPath, root+string(os.PathSeparator)) {
		return Value{}, fmt.Errorf("secret path escapes root: %s", name)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Value{}, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return Value{}, err
	}
	if info.IsDir() {
		return Value{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if mode := info.Mode().Perm(); runtime.GOOS != "windows" && mode&0o077 != 0 {
		return Value{}, fmt.Errorf("secret file %q has loose permissions %o; want no group/other permissions", name, mode)
	}
	bytes, err := os.ReadFile(absPath)
	if err != nil {
		return Value{}, err
	}
	return NewValue(strings.TrimRight(string(bytes), "\r\n")), nil
}

type CompositeProvider struct {
	providers []Provider
}

func NewCompositeProvider(providers ...Provider) *CompositeProvider {
	copyProviders := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			copyProviders = append(copyProviders, provider)
		}
	}
	return &CompositeProvider{providers: copyProviders}
}

func (p *CompositeProvider) Get(ctx context.Context, name string) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	for _, provider := range p.providers {
		value, err := provider.Get(ctx, name)
		if err == nil {
			return value, nil
		}
		if !IsNotFound(err) {
			return Value{}, err
		}
	}
	return Value{}, fmt.Errorf("%w: %s", ErrNotFound, name)
}

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

func envSecretName(name string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
