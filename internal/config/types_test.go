package config

import (
	"testing"
	"time"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		in      string
		bytes   uint64
		percent float64
		isPct   bool
		set     bool
		wantErr bool
	}{
		{in: "", set: false},
		{in: "0", bytes: 0, set: true},
		{in: "512", bytes: 512, set: true},
		{in: "512B", bytes: 512, set: true},
		{in: "1KB", bytes: 1 << 10, set: true},
		{in: "50GB", bytes: 50 << 30, set: true},
		{in: "50gb", bytes: 50 << 30, set: true},
		{in: " 2 GB ", bytes: 2 << 30, set: true},
		{in: "1.5TB", bytes: 3 << 39, set: true},
		{in: "10%", percent: 10, isPct: true, set: true},
		{in: "2.5%", percent: 2.5, isPct: true, set: true},
		{in: "0%", percent: 0, isPct: true, set: true},
		{in: "100%", percent: 100, isPct: true, set: true},
		{in: "101%", wantErr: true},
		{in: "-1%", wantErr: true},
		{in: "-5GB", wantErr: true},
		{in: "GB", wantErr: true},
		{in: "5XB", wantErr: true},
		{in: "5 GiB", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "NaN", wantErr: true},
		{in: "Inf", wantErr: true},
		{in: "99999999TB", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseSize(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseSize(%q) = %+v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSize(%q): %v", tt.in, err)
			}
			if got.IsSet() != tt.set || got.IsPercent() != tt.isPct {
				t.Fatalf("ParseSize(%q) set=%v pct=%v, want set=%v pct=%v", tt.in, got.IsSet(), got.IsPercent(), tt.set, tt.isPct)
			}
			if tt.isPct && got.PercentValue() != tt.percent {
				t.Fatalf("ParseSize(%q) percent = %v, want %v", tt.in, got.PercentValue(), tt.percent)
			}
			if !tt.isPct && got.Resolve(0) != tt.bytes {
				t.Fatalf("ParseSize(%q) bytes = %d, want %d", tt.in, got.Resolve(0), tt.bytes)
			}
		})
	}
}

func TestSizeResolve(t *testing.T) {
	tests := []struct {
		size  Size
		total uint64
		want  uint64
	}{
		{Bytes(100), 1000, 100},
		{Percent(10), 1000, 100},
		{Percent(2.5), 1000, 25},
		{Percent(0), 1000, 0},
		{Percent(100), 1 << 40, 1 << 40},
	}
	for _, tt := range tests {
		if got := tt.size.Resolve(tt.total); got != tt.want {
			t.Errorf("%v.Resolve(%d) = %d, want %d", tt.size, tt.total, got, tt.want)
		}
	}
}

func TestSizeString(t *testing.T) {
	tests := []struct {
		size Size
		want string
	}{
		{Size{}, ""},
		{Bytes(0), "0B"},
		{Bytes(2 << 30), "2GB"},
		{Bytes(1536), "1536B"},
		{Percent(10), "10%"},
		{Percent(2.5), "2.5%"},
	}
	for _, tt := range tests {
		if got := tt.size.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestSizeUnmarshalTOML(t *testing.T) {
	var s Size
	if err := s.UnmarshalTOML(int64(0)); err != nil || !s.IsSet() || s.Resolve(0) != 0 {
		t.Fatalf("int 0: got %+v, %v", s, err)
	}
	if err := s.UnmarshalTOML(int64(-1)); err == nil {
		t.Fatal("negative int: want error")
	}
	if err := s.UnmarshalTOML(1.5); err == nil {
		t.Fatal("float: want error")
	}
	if err := s.UnmarshalTOML("10%"); err != nil || !s.IsPercent() {
		t.Fatalf("string: got %+v, %v", s, err)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "0", want: 0},
		{in: "0s", want: 0},
		{in: "30m", want: 30 * time.Minute},
		{in: "6h", want: 6 * time.Hour},
		{in: "7d", want: 7 * 24 * time.Hour},
		{in: "1d12h", want: 36 * time.Hour},
		{in: "1.5d", want: 36 * time.Hour},
		{in: "1h30m", want: 90 * time.Minute},
		{in: "500ms", want: 500 * time.Millisecond},
		{in: " 15m ", want: 15 * time.Minute},
		{in: "", wantErr: true},
		{in: "7", wantErr: true},
		{in: "-1h", wantErr: true},
		{in: "1w", wantErr: true},
		{in: "d", wantErr: true},
		{in: "1h-", wantErr: true},
		{in: "999999999d", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseDuration(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseDuration(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDuration(%q): %v", tt.in, err)
			}
			if got.Std() != tt.want {
				t.Fatalf("ParseDuration(%q) = %v, want %v", tt.in, got.Std(), tt.want)
			}
		})
	}
}

func TestDurationString(t *testing.T) {
	tests := []struct {
		d    Duration
		want string
	}{
		{Duration(7 * 24 * time.Hour), "7d"},
		{Duration(6 * time.Hour), "6h0m0s"},
		{Duration(36 * time.Hour), "36h0m0s"},
		{0, "0s"},
	}
	for _, tt := range tests {
		if got := tt.d.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}
