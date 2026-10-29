package platform

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/health"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/uow"
)

type RoutedTx = uow.RoutedTx

type UnitOfWork = uow.UnitOfWork

type Tx = uow.Tx

type TxEventMessage = uow.EventMessage

type TxOutboxMessage = uow.OutboxMessage

type RoutedUnitOfWork = uow.RoutedUnitOfWork

type InitializerFunc func(context.Context, InitializerContext) error

type InitializerContext interface {
	DBRouter() *DBRouter
	UnitOfWork() *UnitOfWork
	RoutedUOW() *RoutedUnitOfWork
	Logger() logging.Logger
}

type registeredInitializer struct {
	name string
	fn   InitializerFunc
	err  error
	ran  bool
}

type initializerContext struct {
	runtime *Runtime
}

func (ctx initializerContext) DBRouter() *DBRouter {
	if ctx.runtime == nil {
		return nil
	}
	return ctx.runtime.DBRouter
}

func (ctx initializerContext) UnitOfWork() *UnitOfWork {
	if ctx.runtime == nil {
		return nil
	}
	return ctx.runtime.UnitOfWork
}

func (ctx initializerContext) RoutedUOW() *RoutedUnitOfWork {
	if ctx.runtime == nil {
		return nil
	}
	return ctx.runtime.RoutedUOW
}

func (ctx initializerContext) Logger() logging.Logger {
	if ctx.runtime == nil {
		return nil
	}
	return ctx.runtime.Logger
}

func (r *Runtime) RegisterInitializer(name string, initializer InitializerFunc) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("initializer name is required")
	}
	if initializer == nil {
		return fmt.Errorf("initializer %q is required", name)
	}
	if err := r.beginOperation(); err != nil {
		return err
	}
	defer r.endOperation()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, registered := range r.initializers {
		if registered.name == name {
			return fmt.Errorf("initializer already registered for name %q", name)
		}
	}
	r.initializers = append(r.initializers, registeredInitializer{name: name, fn: initializer})
	if r.Health != nil {
		r.Health.WithReadyCheck(r.initializerReadyCheck(name))
	}
	return nil
}

func (r *Runtime) Initialize(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("platform runtime is not configured")
	}
	if err := r.beginOperation(); err != nil {
		return err
	}
	defer r.endOperation()
	if r.Migrations == nil {
		return fmt.Errorf("platform migration coordinator is not configured")
	}
	if err := r.Migrations.Up(ctx); err != nil {
		return err
	}
	return r.runInitializers(ctx)
}

func (r *Runtime) runInitializers(ctx context.Context) error {
	r.mu.Lock()
	initializers := append([]registeredInitializer(nil), r.initializers...)
	r.mu.Unlock()
	var firstErr error
	for _, initializer := range initializers {
		if initializer.ran && initializer.err == nil {
			continue
		}
		if initializer.fn == nil {
			err := fmt.Errorf("initializer %q is required", initializer.name)
			r.recordInitializerResult(initializer.name, err, false)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		err := initializer.fn(ctx, initializerContext{runtime: r})
		r.recordInitializerResult(initializer.name, err, true)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("run initializer %q: %w", initializer.name, err)
		}
	}
	return firstErr
}

func (r *Runtime) recordInitializerResult(name string, err error, ran bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.initializers {
		if r.initializers[i].name == name {
			r.initializers[i].err = err
			r.initializers[i].ran = ran
			return
		}
	}
}

func (r *Runtime) initializerReadyCheck(name string) health.ReadyCheck {
	return func(context.Context) health.Check {
		r.mu.RLock()
		defer r.mu.RUnlock()
		for _, initializer := range r.initializers {
			if initializer.name != name {
				continue
			}
			check := health.Check{Name: "initializer." + name, OK: initializer.ran && initializer.err == nil}
			if initializer.err != nil {
				check.Message = initializer.err.Error()
			} else if !initializer.ran {
				check.Message = "initializer has not run"
			}
			return check
		}
		return health.Check{Name: "initializer." + name, OK: false, Message: "initializer is not registered"}
	}
}
