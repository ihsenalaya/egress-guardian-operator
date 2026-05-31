package observer

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// HubbleGRPCFlowSource streams egress flows from Hubble Relay via gRPC.
// It uses raw protobuf bytes decoded via a minimal hand-rolled client to
// avoid the heavy github.com/cilium/cilium dependency (per design decision 6).
type HubbleGRPCFlowSource struct {
	Address   string // e.g. "hubble-relay.kube-system.svc.cluster.local:80"
	Namespace string // filter to this namespace (empty = all)
	log       logr.Logger
}

// NewHubbleGRPCFlowSource creates a HubbleGRPCFlowSource.
func NewHubbleGRPCFlowSource(address, namespace string, log logr.Logger) *HubbleGRPCFlowSource {
	return &HubbleGRPCFlowSource{
		Address:   address,
		Namespace: namespace,
		log:       log,
	}
}

// Observe connects to Hubble Relay and streams egress flows until ctx is cancelled.
// The gRPC method is /observer.Observer/GetFlows (Hubble Observer API).
func (h *HubbleGRPCFlowSource) Observe(ctx context.Context, out chan<- Flow) error {
	conn, err := grpc.DialContext(ctx, h.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithTimeout(10*time.Second),
	)
	if err != nil {
		return fmt.Errorf("dial hubble relay %s: %w", h.Address, err)
	}
	defer conn.Close()

	h.log.Info("connected to Hubble Relay", "address", h.Address)

	// Build the GetFlows request as raw proto bytes.
	// We use the minimal encoding to avoid importing cilium/cilium.
	// Proto field layout for observer.GetFlowsRequest:
	//   field 7 = follow (bool, varint) → 0x38 0x01
	//   field 5 = whitelist (repeated FlowFilter)
	//     FlowFilter field 6 = traffic_direction (repeated uint32) → EGRESS = 2
	reqBytes := buildGetFlowsRequest()

	// Invoke streaming RPC via the raw ClientStream API.
	sd := &grpc.StreamDesc{ServerStreams: true}
	stream, err := conn.NewStream(ctx, sd, "/observer.Observer/GetFlows")
	if err != nil {
		return fmt.Errorf("open GetFlows stream: %w", err)
	}
	if err := stream.SendMsg(&rawProtoMsg{data: reqBytes}); err != nil {
		return fmt.Errorf("send GetFlows request: %w", err)
	}
	if err := stream.CloseSend(); err != nil {
		return fmt.Errorf("close send: %w", err)
	}

	for {
		msg := &rawProtoMsg{}
		if err := stream.RecvMsg(msg); err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("recv flow: %w", err)
		}
		f, ok := decodeGetFlowsResponse(msg.data)
		if !ok {
			continue
		}
		if h.Namespace != "" && f.SourceNamespace != h.Namespace {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case out <- f:
		}
	}
}

// rawProtoMsg implements proto.Message via the grpc codec interface.
type rawProtoMsg struct{ data []byte }

func (r *rawProtoMsg) ProtoMessage()             {}
func (r *rawProtoMsg) Reset()                    {}
func (r *rawProtoMsg) String() string            { return "" }
func (r *rawProtoMsg) Marshal() ([]byte, error)  { return r.data, nil }
func (r *rawProtoMsg) Unmarshal(b []byte) error  { r.data = b; return nil }
func (r *rawProtoMsg) Size() int                 { return len(r.data) }

// buildGetFlowsRequest encodes a minimal GetFlowsRequest protobuf:
//   follow=true, whitelist=[FlowFilter{traffic_direction=EGRESS}]
func buildGetFlowsRequest() []byte {
	// field 7 (follow) = true → tag=0x38, value=0x01
	// field 5 (whitelist FlowFilter) → tag=0x2A, length-delimited
	//   FlowFilter field 6 (traffic_direction) = 2 (EGRESS) → tag=0x30, value=0x02
	filterBytes := []byte{0x30, 0x02} // traffic_direction=EGRESS
	req := []byte{
		0x38, 0x01, // follow=true
		0x2A, byte(len(filterBytes)),
	}
	return append(req, filterBytes...)
}

// decodeGetFlowsResponse parses a raw GetFlowsResponse proto into a Flow.
// This is a best-effort decoder; unknown fields are silently ignored.
func decodeGetFlowsResponse(data []byte) (Flow, bool) {
	// GetFlowsResponse field 1 = flow (Flow message)
	// We look for field tag 0x0A (field=1, wire=2 length-delimited).
	flowBytes, ok := extractField(data, 1)
	if !ok {
		return Flow{}, false
	}
	return decodeFlow(flowBytes)
}

// decodeFlow decodes a Hubble Flow proto message into our internal Flow type.
func decodeFlow(data []byte) (Flow, bool) {
	var f Flow
	f.Timestamp = time.Now()

	// Field 1 = time (Timestamp message) — skip for brevity, use Now()
	// Field 3 = source (Endpoint)
	// Field 4 = destination (Endpoint)
	// Field 5 = l4 (Layer4)
	// Field 9 = verdict (Verdict enum)
	// Field 26 = traffic_direction

	srcBytes, _ := extractField(data, 3)
	dstBytes, _ := extractField(data, 4)
	l4Bytes, _ := extractField(data, 5)
	verdict, _ := extractVarint(data, 9)
	dnsBytes, _ := extractField(data, 12) // DNS L7

	// Decode source endpoint: field 1=namespace, field 3=pod_name
	f.SourceNamespace, _ = extractString(srcBytes, 1)
	f.SourcePod, _ = extractString(srcBytes, 3)
	f.SourceWorkload = fmt.Sprintf("Pod/%s", f.SourcePod)

	// Decode destination endpoint
	f.DestIP, _ = extractString(dstBytes, 1)
	fqdn, _ := extractString(dstBytes, 6) // DNS name from endpoint labels
	if fqdn != "" {
		f.DestFQDN = fqdn
	}

	// Decode L4: field 1=TCP, field 2=UDP
	tcpBytes, hasTCP := extractField(l4Bytes, 1)
	udpBytes, hasUDP := extractField(l4Bytes, 2)
	if hasTCP {
		port, _ := extractVarint(tcpBytes, 2) // destination_port
		f.DestPort = uint32(port)
		f.Protocol = "TCP"
	} else if hasUDP {
		port, _ := extractVarint(udpBytes, 2)
		f.DestPort = uint32(port)
		f.Protocol = "UDP"
	}

	// Decode verdict: 3=FORWARDED, 4=DROPPED, 5=AUDIT
	switch verdict {
	case 3:
		f.Verdict = "FORWARDED"
	case 4:
		f.Verdict = "DROPPED"
	case 5:
		f.Verdict = "AUDIT"
	default:
		f.Verdict = "UNKNOWN"
	}

	// Attempt DNS FQDN resolution from DNS L7 observation
	if len(dnsBytes) > 0 {
		name, _ := extractString(dnsBytes, 3) // dns.query
		if name != "" {
			f.DestFQDN = name
		}
	}

	if f.SourceNamespace == "" {
		return f, false
	}
	return f, true
}

// --- minimal proto decoder helpers ---

func extractField(data []byte, fieldNum uint64) ([]byte, bool) {
	i := 0
	for i < len(data) {
		tag, n := decodeVarint(data[i:])
		if n == 0 {
			break
		}
		i += n
		wireType := tag & 0x7
		fn := tag >> 3

		switch wireType {
		case 0: // varint
			_, n2 := decodeVarint(data[i:])
			if n2 == 0 {
				return nil, false
			}
			if fn == fieldNum {
				return nil, false // not length-delimited
			}
			i += n2
		case 2: // length-delimited
			length, n2 := decodeVarint(data[i:])
			if n2 == 0 {
				return nil, false
			}
			i += n2
			end := i + int(length)
			if end > len(data) {
				return nil, false
			}
			if fn == fieldNum {
				return data[i:end], true
			}
			i = end
		default:
			return nil, false
		}
	}
	return nil, false
}

func extractVarint(data []byte, fieldNum uint64) (uint64, bool) {
	i := 0
	for i < len(data) {
		tag, n := decodeVarint(data[i:])
		if n == 0 {
			break
		}
		i += n
		wireType := tag & 0x7
		fn := tag >> 3
		if wireType == 0 {
			v, n2 := decodeVarint(data[i:])
			if n2 == 0 {
				return 0, false
			}
			if fn == fieldNum {
				return v, true
			}
			i += n2
		} else if wireType == 2 {
			length, n2 := decodeVarint(data[i:])
			if n2 == 0 {
				return 0, false
			}
			i += n2 + int(length)
		} else {
			return 0, false
		}
	}
	return 0, false
}

func extractString(data []byte, fieldNum uint64) (string, bool) {
	b, ok := extractField(data, fieldNum)
	if !ok {
		return "", false
	}
	return string(b), true
}

func decodeVarint(data []byte) (uint64, int) {
	var x uint64
	var s uint
	for i, b := range data {
		if i == 10 {
			return 0, 0
		}
		if b < 0x80 {
			return x | uint64(b)<<s, i + 1
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, 0
}
