// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pprof

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"reflect"
	"runtime"
	"runtime/pprof/internal/profile"
	"testing"
)

// translateCPUProfile parses binary CPU profiling stack trace data
// generated by runtime.CPUProfile() into a profile struct.
// This is only used for testing. Real conversions stream the
// data into the profileBuilder as it becomes available.
func translateCPUProfile(data []uint64) (*profile.Profile, error) {
	var buf bytes.Buffer
	b := newProfileBuilder(&buf)
	if err := b.addCPUData(data, nil); err != nil {
		return nil, err
	}
	b.build()
	return profile.Parse(&buf)
}

// fmtJSON returns a pretty-printed JSON form for x.
// It works reasonbly well for printing protocol-buffer
// data structures like profile.Profile.
func fmtJSON(x interface{}) string {
	js, _ := json.MarshalIndent(x, "", "\t")
	return string(js)
}

func TestConvertCPUProfileEmpty(t *testing.T) {
	// A test server with mock cpu profile data.
	var buf bytes.Buffer

	b := []uint64{3, 0, 2000} // empty profile with 2ms sample period
	p, err := translateCPUProfile(b)
	if err != nil {
		t.Fatalf("translateCPUProfile: %v", err)
	}
	if err := p.Write(&buf); err != nil {
		t.Fatalf("writing profile: %v", err)
	}

	p, err = profile.Parse(&buf)
	if err != nil {
		t.Fatalf("profile.Parse: %v", err)
	}

	// Expected PeriodType and SampleType.
	periodType := &profile.ValueType{Type: "cpu", Unit: "nanoseconds"}
	sampleType := []*profile.ValueType{
		{Type: "samples", Unit: "count"},
		{Type: "cpu", Unit: "nanoseconds"},
	}

	checkProfile(t, p, 2000*1000, periodType, sampleType, nil)
}

func f1() { f1() }
func f2() { f2() }

// testPCs returns two PCs and two corresponding memory mappings
// to use in test profiles.
func testPCs(t *testing.T) (addr1, addr2 uint64, map1, map2 *profile.Mapping) {
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		// Figure out two addresses from /proc/self/maps.
		mmap, err := ioutil.ReadFile("/proc/self/maps")
		if err != nil {
			t.Fatal(err)
		}
		mprof := &profile.Profile{}
		if err = mprof.ParseMemoryMap(bytes.NewReader(mmap)); err != nil {
			t.Fatalf("parsing /proc/self/maps: %v", err)
		}
		if len(mprof.Mapping) < 2 {
			// It is possible for a binary to only have 1 executable
			// region of memory.
			t.Skipf("need 2 or more mappings, got %v", len(mprof.Mapping))
		}
		addr1 = mprof.Mapping[0].Start
		map1 = mprof.Mapping[0]
		addr2 = mprof.Mapping[1].Start
		map2 = mprof.Mapping[1]
	} else {
		addr1 = uint64(funcPC(f1))
		addr2 = uint64(funcPC(f2))
	}
	return
}

func TestConvertCPUProfile(t *testing.T) {
	addr1, addr2, map1, map2 := testPCs(t)

	b := []uint64{
		3, 0, 2000, // periodMs = 2000
		5, 0, 10, uint64(addr1), uint64(addr1 + 2), // 10 samples in addr1
		5, 0, 40, uint64(addr2), uint64(addr2 + 2), // 40 samples in addr2
		5, 0, 10, uint64(addr1), uint64(addr1 + 2), // 10 samples in addr1
	}
	p, err := translateCPUProfile(b)
	if err != nil {
		t.Fatalf("translating profile: %v", err)
	}
	period := int64(2000 * 1000)
	periodType := &profile.ValueType{Type: "cpu", Unit: "nanoseconds"}
	sampleType := []*profile.ValueType{
		{Type: "samples", Unit: "count"},
		{Type: "cpu", Unit: "nanoseconds"},
	}
	samples := []*profile.Sample{
		{Value: []int64{20, 20 * 2000 * 1000}, Location: []*profile.Location{
			{ID: 1, Mapping: map1, Address: addr1},
			{ID: 2, Mapping: map1, Address: addr1 + 1},
		}},
		{Value: []int64{40, 40 * 2000 * 1000}, Location: []*profile.Location{
			{ID: 3, Mapping: map2, Address: addr2},
			{ID: 4, Mapping: map2, Address: addr2 + 1},
		}},
	}
	checkProfile(t, p, period, periodType, sampleType, samples)
}

func checkProfile(t *testing.T, p *profile.Profile, period int64, periodType *profile.ValueType, sampleType []*profile.ValueType, samples []*profile.Sample) {
	if p.Period != period {
		t.Fatalf("p.Period = %d, want %d", p.Period, period)
	}
	if !reflect.DeepEqual(p.PeriodType, periodType) {
		t.Fatalf("p.PeriodType = %v\nwant = %v", fmtJSON(p.PeriodType), fmtJSON(periodType))
	}
	if !reflect.DeepEqual(p.SampleType, sampleType) {
		t.Fatalf("p.SampleType = %v\nwant = %v", fmtJSON(p.SampleType), fmtJSON(sampleType))
	}
	// Clear line info since it is not in the expected samples.
	// If we used f1 and f2 above, then the samples will have line info.
	for _, s := range p.Sample {
		for _, l := range s.Location {
			l.Line = nil
		}
	}
	if fmtJSON(p.Sample) != fmtJSON(samples) { // ignore unexported fields
		if len(p.Sample) == len(samples) {
			for i := range p.Sample {
				if !reflect.DeepEqual(p.Sample[i], samples[i]) {
					t.Errorf("sample %d = %v\nwant = %v\n", i, fmtJSON(p.Sample[i]), fmtJSON(samples[i]))
				}
			}
			if t.Failed() {
				t.FailNow()
			}
		}
		t.Fatalf("p.Sample = %v\nwant = %v", fmtJSON(p.Sample), fmtJSON(samples))
	}
}

type fakeFunc struct {
	name   string
	file   string
	lineno int
}

func (f *fakeFunc) Name() string {
	return f.name
}
func (f *fakeFunc) FileLine(uintptr) (string, int) {
	return f.file, f.lineno
}

/*
// TestRuntimeFunctionTrimming tests if symbolize trims runtime functions as intended.
func TestRuntimeRunctionTrimming(t *testing.T) {
	fakeFuncMap := map[uintptr]*fakeFunc{
		0x10: &fakeFunc{"runtime.goexit", "runtime.go", 10},
		0x20: &fakeFunc{"runtime.other", "runtime.go", 20},
		0x30: &fakeFunc{"foo", "foo.go", 30},
		0x40: &fakeFunc{"bar", "bar.go", 40},
	}
	backupFuncForPC := funcForPC
	funcForPC = func(pc uintptr) function {
		return fakeFuncMap[pc]
	}
	defer func() {
		funcForPC = backupFuncForPC
	}()
	testLoc := []*profile.Location{
		{ID: 1, Address: 0x10},
		{ID: 2, Address: 0x20},
		{ID: 3, Address: 0x30},
		{ID: 4, Address: 0x40},
	}
	testProfile := &profile.Profile{
		Sample: []*profile.Sample{
			{Location: []*profile.Location{testLoc[0], testLoc[1], testLoc[3], testLoc[2]}},
			{Location: []*profile.Location{testLoc[1], testLoc[3], testLoc[2]}},
			{Location: []*profile.Location{testLoc[3], testLoc[2], testLoc[1]}},
			{Location: []*profile.Location{testLoc[3], testLoc[2], testLoc[0]}},
			{Location: []*profile.Location{testLoc[0], testLoc[1], testLoc[3], testLoc[0]}},
		},
		Location: testLoc,
	}
	testProfiles := make([]*profile.Profile, 2)
	testProfiles[0] = testProfile.Copy()
	testProfiles[1] = testProfile.Copy()
	// Test case for profilez.
	testProfiles[0].PeriodType = &profile.ValueType{Type: "cpu", Unit: "nanoseconds"}
	// Test case for heapz.
	testProfiles[1].PeriodType = &profile.ValueType{Type: "space", Unit: "bytes"}
	wantFunc := []*profile.Function{
		{ID: 1, Name: "runtime.goexit", SystemName: "runtime.goexit", Filename: "runtime.go"},
		{ID: 2, Name: "runtime.other", SystemName: "runtime.other", Filename: "runtime.go"},
		{ID: 3, Name: "foo", SystemName: "foo", Filename: "foo.go"},
		{ID: 4, Name: "bar", SystemName: "bar", Filename: "bar.go"},
	}
	wantLoc := []*profile.Location{
		{ID: 1, Address: 0x10, Line: []profile.Line{{Function: wantFunc[0], Line: 10}}},
		{ID: 2, Address: 0x20, Line: []profile.Line{{Function: wantFunc[1], Line: 20}}},
		{ID: 3, Address: 0x30, Line: []profile.Line{{Function: wantFunc[2], Line: 30}}},
		{ID: 4, Address: 0x40, Line: []profile.Line{{Function: wantFunc[3], Line: 40}}},
	}
	wantProfiles := []*profile.Profile{
		{
			PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
			Sample: []*profile.Sample{
				{Location: []*profile.Location{wantLoc[1], wantLoc[3], wantLoc[2]}},
				{Location: []*profile.Location{wantLoc[1], wantLoc[3], wantLoc[2]}},
				{Location: []*profile.Location{wantLoc[3], wantLoc[2], wantLoc[1]}},
				{Location: []*profile.Location{wantLoc[3], wantLoc[2]}},
				{Location: []*profile.Location{wantLoc[1], wantLoc[3]}},
			},
			Location: wantLoc,
			Function: wantFunc,
		},
		{
			PeriodType: &profile.ValueType{Type: "space", Unit: "bytes"},
			Sample: []*profile.Sample{
				{Location: []*profile.Location{wantLoc[3], wantLoc[2]}},
				{Location: []*profile.Location{wantLoc[3], wantLoc[2]}},
				{Location: []*profile.Location{wantLoc[3], wantLoc[2], wantLoc[1]}},
				{Location: []*profile.Location{wantLoc[3], wantLoc[2]}},
				{Location: []*profile.Location{wantLoc[3]}},
			},
			Location: wantLoc,
			Function: wantFunc,
		},
	}
	for i := 0; i < 2; i++ {
		symbolize(testProfiles[i])
		if !reflect.DeepEqual(testProfiles[i], wantProfiles[i]) {
			t.Errorf("incorrect trimming (testcase = %d): got {%v}, want {%v}", i, testProfiles[i], wantProfiles[i])
		}
	}
}
*/
