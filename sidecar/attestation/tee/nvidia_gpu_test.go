package tee

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type gpuFrame struct {
	index  uint32
	arch   uint32
	uuid   string
	report []byte
	cec    []byte
	cert   []byte
}

func encodeGPUFrames(t *testing.T, frames ...gpuFrame) []byte {
	t.Helper()

	var out bytes.Buffer
	if err := binary.Write(&out, binary.LittleEndian, uint32(len(frames))); err != nil {
		t.Fatal(err)
	}
	for _, frame := range frames {
		if frame.arch == 0 {
			frame.arch = 10
		}
		if frame.uuid == "" {
			frame.uuid = fmt.Sprintf("GPU-00000000-0000-0000-0000-%012x", frame.index)
		}
		if err := binary.Write(&out, binary.LittleEndian, frame.index); err != nil {
			t.Fatal(err)
		}
		if err := binary.Write(&out, binary.LittleEndian, frame.arch); err != nil {
			t.Fatal(err)
		}
		if err := binary.Write(&out, binary.LittleEndian, uint32(len(frame.uuid))); err != nil {
			t.Fatal(err)
		}
		_, _ = out.WriteString(frame.uuid)
		if err := binary.Write(&out, binary.LittleEndian, uint32(len(frame.report))); err != nil {
			t.Fatal(err)
		}
		_, _ = out.Write(frame.report)
		if err := binary.Write(&out, binary.LittleEndian, uint32(len(frame.cec))); err != nil {
			t.Fatal(err)
		}
		_, _ = out.Write(frame.cec)
		if err := binary.Write(&out, binary.LittleEndian, uint32(len(frame.cert))); err != nil {
			t.Fatal(err)
		}
		_, _ = out.Write(frame.cert)
	}
	return out.Bytes()
}

func TestParseMultiGPUOutputValidMultiple(t *testing.T) {
	data := encodeGPUFrames(t,
		gpuFrame{index: 2, report: []byte("report-2"), cec: []byte("cec-2"), cert: []byte("cert-2")},
		gpuFrame{index: 7, report: []byte("report-7"), cert: []byte("cert-7")},
	)

	reports, err := parseMultiGPUOutput(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []GPUDeviceReport{
		{
			DeviceIndex:       2,
			Architecture:      "BLACKWELL",
			UUID:              "GPU-00000000-0000-0000-0000-000000000002",
			Report:            []byte("report-2cec-2cert-2"),
			AttestationReport: []byte("report-2"),
			CECReport:         []byte("cec-2"),
			CertificateChain:  []byte("cert-2"),
		},
		{
			DeviceIndex:       7,
			Architecture:      "BLACKWELL",
			UUID:              "GPU-00000000-0000-0000-0000-000000000007",
			Report:            []byte("report-7cert-7"),
			AttestationReport: []byte("report-7"),
			CertificateChain:  []byte("cert-7"),
		},
	}
	if !reflect.DeepEqual(want, reports) {
		t.Fatalf("reports mismatch\nwant: %#v\n got: %#v", want, reports)
	}
}

func TestParseMultiGPUOutputRejectsMalformedFraming(t *testing.T) {
	valid := encodeGPUFrames(t, gpuFrame{index: 0, report: []byte("report"), cert: []byte("cert")})
	const defaultUUID = "GPU-00000000-0000-0000-0000-000000000000"
	const frameHeaderSize = 4 + 4 + 4 + 4 + len(defaultUUID)

	withoutCertSize := encodeGPUFrames(t, gpuFrame{index: 0, report: []byte("report")})
	missingCertSize := append([]byte(nil), withoutCertSize[:len(withoutCertSize)-4]...)
	truncatedCert := append([]byte(nil), missingCertSize...)
	var certHeader bytes.Buffer
	if err := binary.Write(&certHeader, binary.LittleEndian, uint32(8)); err != nil {
		t.Fatal(err)
	}
	truncatedCert = append(truncatedCert, certHeader.Bytes()...)
	truncatedCert = append(truncatedCert, []byte("short")...)

	reportSizeOffset := frameHeaderSize
	truncatedReport := append([]byte(nil), valid[:reportSizeOffset+4]...)
	binary.LittleEndian.PutUint32(truncatedReport[reportSizeOffset:reportSizeOffset+4], 100)
	truncatedReport = append(truncatedReport, []byte("short")...)

	cecSizeOffset := reportSizeOffset + 4 + len("report")
	missingCECSize := append([]byte(nil), valid[:cecSizeOffset]...)
	truncatedCEC := encodeGPUFrames(t, gpuFrame{
		index: 0, report: []byte("report"), cec: []byte("short"), cert: []byte("cert"),
	})
	truncatedCEC = append([]byte(nil), truncatedCEC[:cecSizeOffset+4+len("short")]...)
	binary.LittleEndian.PutUint32(truncatedCEC[cecSizeOffset:cecSizeOffset+4], 8)

	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{name: "zero devices", data: []byte{0, 0, 0, 0}, wantErr: "0 device reports"},
		{name: "count exceeds frames", data: []byte{2, 0, 0, 0}, wantErr: "exceeds framed payload size"},
		{name: "missing device index", data: []byte{1, 0, 0, 0}, wantErr: "exceeds framed payload size"},
		{name: "missing report size", data: []byte{1, 0, 0, 0, 0, 0, 0, 0}, wantErr: "exceeds framed payload size"},
		{name: "empty report", data: encodeGPUFrames(t, gpuFrame{index: 0}), wantErr: "attestation report is empty"},
		{name: "empty certificate chain", data: withoutCertSize, wantErr: "certificate chain is empty"},
		{name: "unsupported architecture", data: encodeGPUFrames(t,
			gpuFrame{index: 0, arch: 8, report: []byte("report"), cert: []byte("cert")},
		), wantErr: "unsupported NVIDIA device architecture"},
		{name: "invalid UUID", data: encodeGPUFrames(t,
			gpuFrame{index: 0, uuid: "GPU-not-a-uuid", report: []byte("report"), cert: []byte("cert")},
		), wantErr: "invalid NVML UUID"},
		{name: "truncated report", data: truncatedReport, wantErr: "report data"},
		{name: "missing CEC size", data: missingCECSize, wantErr: "CEC size"},
		{name: "truncated CEC", data: truncatedCEC, wantErr: "CEC data"},
		{name: "missing required certificate size", data: missingCertSize, wantErr: "certificate size"},
		{name: "truncated certificate", data: truncatedCert, wantErr: "certificate data"},
		{name: "trailing bytes", data: append(append([]byte(nil), valid...), 0xff), wantErr: "trailing bytes"},
		{name: "duplicate device index", data: encodeGPUFrames(t,
			gpuFrame{index: 3, report: []byte("one"), cert: []byte("cert-one")},
			gpuFrame{index: 3, report: []byte("two"), cert: []byte("cert-two")},
		), wantErr: "duplicate device index"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseMultiGPUOutput(tt.data)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestNvidiaArchitectureName(t *testing.T) {
	tests := []struct {
		value uint32
		want  string
	}{
		{value: 9, want: "HOPPER"},
		{value: 10, want: "BLACKWELL"},
	}

	for _, tt := range tests {
		got, err := nvidiaArchitectureName(tt.value)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Fatalf("architecture %d: want %q, got %q", tt.value, tt.want, got)
		}
	}
}

func TestParseMultiGPUOutputOwnsFramedData(t *testing.T) {
	data := encodeGPUFrames(t, gpuFrame{
		index:  1,
		report: []byte("report"),
		cec:    []byte("cec"),
		cert:   []byte("cert"),
	})

	reports, err := parseMultiGPUOutput(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := range data {
		data[i] = 0
	}

	want := GPUDeviceReport{
		DeviceIndex:       1,
		Architecture:      "BLACKWELL",
		UUID:              "GPU-00000000-0000-0000-0000-000000000001",
		Report:            []byte("reportceccert"),
		AttestationReport: []byte("report"),
		CECReport:         []byte("cec"),
		CertificateChain:  []byte("cert"),
	}
	if !reflect.DeepEqual(want, reports[0]) {
		t.Fatalf("report aliases input\nwant: %#v\n got: %#v", want, reports[0])
	}
}
