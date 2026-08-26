package domain

import "testing"

func TestFlowBasics(t *testing.T) {
	c, e := NewCase("x", "b", "m", "p", "o", "r", "a")
	if e != nil {
		t.Fatal(e)
	}
	if e = c.SubmitPlan(Plan{Groups: []string{"A", "B"}, TempMin: 1, TempMax: 2, HumMin: 1, HumMax: 2, MinExposure: 10, Metrics: []string{"ph"}}, "1"); e != nil {
		t.Fatal(e)
	}
	c.AddConditioning(ConditioningReading{Temperature: 1.5, Humidity: 1.5, ExposedMinutes: 5}, "2")
	c.AddConditioning(ConditioningReading{Temperature: 1.5, Humidity: 1.5, ExposedMinutes: 5}, "3")
	if e = c.ConfirmConditioning("4"); e != nil {
		t.Fatal(e)
	}
}
