package pb_test

import (
	"testing"

	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Enhancement 529-02 contract: gRPC PutMemory idempotency fields, user and admin.

type putMemoryMessage struct {
	name string
	desc protoreflect.MessageDescriptor
	new  func() proto.Message
}

func putMemoryMessages() []putMemoryMessage {
	return []putMemoryMessage{
		{"PutMemoryRequest", (&pb.PutMemoryRequest{}).ProtoReflect().Descriptor(), func() proto.Message { return &pb.PutMemoryRequest{} }},
		{"AdminPutMemoryRequest", (&pb.AdminPutMemoryRequest{}).ProtoReflect().Descriptor(), func() proto.Message { return &pb.AdminPutMemoryRequest{} }},
	}
}

func TestPutMemoryRequestIdempotencyFieldNumbers(t *testing.T) {
	t.Parallel()
	for _, msg := range putMemoryMessages() {
		t.Run(msg.name, func(t *testing.T) {
			for _, want := range []struct {
				name   protoreflect.Name
				number protoreflect.FieldNumber
				kind   protoreflect.Kind
			}{
				{"revision_id", 9, protoreflect.StringKind},
				{"expected_revision_id", 10, protoreflect.StringKind},
				{"revision", 11, protoreflect.Int64Kind},
			} {
				field := msg.desc.Fields().ByName(want.name)
				require.NotNil(t, field, want.name)
				require.Equal(t, want.number, field.Number(), want.name)
				require.Equal(t, want.kind, field.Kind(), want.name)
				// proto3 `optional`: explicit presence is required so an unset field differs from zero/"".
				require.True(t, field.HasPresence(), want.name)
				require.True(t, field.HasOptionalKeyword(), want.name)
				require.Equal(t, field, msg.desc.Fields().ByNumber(want.number))
			}
		})
	}
}

// Existing wire numbers must not move: 1-8 keep their meaning (7 is `reserved actor` on the user
// message and `justification` on the admin message).
func TestPutMemoryRequestExistingFieldNumbersUnchanged(t *testing.T) {
	t.Parallel()
	common := map[protoreflect.Name]protoreflect.FieldNumber{
		"namespace": 1, "key": 2, "value": 3, "ttl_seconds": 4, "index": 5, "expected_revision": 6, "kind": 8,
	}
	for _, msg := range putMemoryMessages() {
		t.Run(msg.name, func(t *testing.T) {
			for name, number := range common {
				field := msg.desc.Fields().ByName(name)
				require.NotNil(t, field, name)
				require.Equal(t, number, field.Number(), name)
			}
		})
	}
	user, admin := putMemoryMessages()[0].desc, putMemoryMessages()[1].desc
	require.Nil(t, user.Fields().ByNumber(7))
	require.True(t, user.ReservedRanges().Has(7))
	require.True(t, user.ReservedNames().Has("actor"))
	require.Equal(t, protoreflect.Name("justification"), admin.Fields().ByNumber(7).Name())
}

func TestPutMemoryRequestIdempotencyFieldsHavePresenceOnTheWire(t *testing.T) {
	t.Parallel()
	for _, msg := range putMemoryMessages() {
		t.Run(msg.name, func(t *testing.T) {
			empty := msg.new()
			for _, name := range []protoreflect.Name{"revision_id", "expected_revision_id", "revision"} {
				require.False(t, empty.ProtoReflect().Has(msg.desc.Fields().ByName(name)), name)
			}

			set := msg.new()
			r := set.ProtoReflect()
			r.Set(msg.desc.Fields().ByName("revision_id"), protoreflect.ValueOfString("6f1c1d0a-0000-4000-8000-000000000001"))
			r.Set(msg.desc.Fields().ByName("expected_revision_id"), protoreflect.ValueOfString("6f1c1d0a-0000-4000-8000-000000000002"))
			// A present zero value must survive the wire: the handler has to reject revision != 1.
			r.Set(msg.desc.Fields().ByName("revision"), protoreflect.ValueOfInt64(0))

			data, err := proto.Marshal(set)
			require.NoError(t, err)
			decoded := msg.new()
			require.NoError(t, proto.Unmarshal(data, decoded))
			d := decoded.ProtoReflect()
			require.Equal(t, "6f1c1d0a-0000-4000-8000-000000000001", d.Get(msg.desc.Fields().ByName("revision_id")).String())
			require.Equal(t, "6f1c1d0a-0000-4000-8000-000000000002", d.Get(msg.desc.Fields().ByName("expected_revision_id")).String())
			require.True(t, d.Has(msg.desc.Fields().ByName("revision")))
			require.EqualValues(t, 0, d.Get(msg.desc.Fields().ByName("revision")).Int())

			// An empty revision_id is present-but-empty and distinguishable from absent (handler: INVALID_ARGUMENT).
			blank := msg.new()
			blank.ProtoReflect().Set(msg.desc.Fields().ByName("revision_id"), protoreflect.ValueOfString(""))
			data, err = proto.Marshal(blank)
			require.NoError(t, err)
			decodedBlank := msg.new()
			require.NoError(t, proto.Unmarshal(data, decodedBlank))
			require.True(t, decodedBlank.ProtoReflect().Has(msg.desc.Fields().ByName("revision_id")))
		})
	}
}

func TestPutMemoryRequestGeneratedAccessors(t *testing.T) {
	t.Parallel()
	id, expected, rev := "a", "b", int64(1)
	user := &pb.PutMemoryRequest{RevisionId: &id, ExpectedRevisionId: &expected, Revision: &rev}
	require.Equal(t, "a", user.GetRevisionId())
	require.Equal(t, "b", user.GetExpectedRevisionId())
	require.EqualValues(t, 1, user.GetRevision())
	admin := &pb.AdminPutMemoryRequest{RevisionId: &id, ExpectedRevisionId: &expected, Revision: &rev}
	require.Equal(t, "a", admin.GetRevisionId())
	require.Equal(t, "b", admin.GetExpectedRevisionId())
	require.EqualValues(t, 1, admin.GetRevision())
}
