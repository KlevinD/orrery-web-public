package outbound

import (
	"context"
	"errors"
	"testing"
)

func TestAddDeliveryValidatorPreservesExistingEligibilityGuard(t *testing.T) {
	for _, existingErr := range []error{nil, errors.New("eligibility store unavailable")} {
		service := NewService(nil, nil, nil)
		called := false
		service.SetDeliveryValidator(func(context.Context, Delivery) (bool, error) { return false, existingErr })
		service.AddDeliveryValidator(func(context.Context, Delivery) (bool, error) { called = true; return true, nil })
		allowed, err := service.validator(context.Background(), Delivery{})
		if allowed || called || !errors.Is(err, existingErr) {
			t.Fatalf("prior guard bypassed: allowed=%v called=%v err=%v", allowed, called, err)
		}
	}
	service := NewService(nil, nil, nil)
	service.AddDeliveryValidator(func(context.Context, Delivery) (bool, error) { return true, nil })
	service.AddDeliveryValidator(func(context.Context, Delivery) (bool, error) { return false, nil })
	if allowed, err := service.validator(context.Background(), Delivery{}); allowed || err != nil {
		t.Fatalf("later guard bypassed: %v %v", allowed, err)
	}
}
