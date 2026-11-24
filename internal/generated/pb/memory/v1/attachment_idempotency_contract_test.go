package pb_test

import (
	"testing"

	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Enhancement 529-03 contract: caller-supplied attachment IDs on the gRPC upload requests.

type attachmentIDMessage struct {
	name string
	desc protoreflect.MessageDescriptor
	new  func() proto.Message
	// existing maps each field that predates the ID to its wire number.
	existing map[protoreflect.Name]protoreflect.FieldNumber
}

func attachmentIDMessages() []attachmentIDMessage {
	return []attachmentIDMessage{
		{
			"UploadMetadata", (&pb.UploadMetadata{}).ProtoReflect().Descriptor(),
			func() proto.Message { return &pb.UploadMetadata{} },
			map[protoreflect.Name]protoreflect.FieldNumber{"filename": 1, "content_type": 2, "expires_in": 3},
		},
		{
			"CreateAttachmentFromUrlRequest", (&pb.CreateAttachmentFromUrlRequest{}).ProtoReflect().Descriptor(),
			func() proto.Message { return &pb.CreateAttachmentFromUrlRequest{} },
			map[protoreflect.Name]protoreflect.FieldNumber{"source_url": 1, "content_type": 2, "filename": 3},
		},
	}
}

func TestAttachmentRequestIDFieldIsOptionalString(t *testing.T) {
	t.Parallel()
	for _, msg := range attachmentIDMessages() {
		t.Run(msg.name, func(t *testing.T) {
			field := msg.desc.Fields().ByName("id")
			require.NotNil(t, field, "id")
			// Next free number after the existing fields; nothing is renumbered or reused.
			require.Equal(t, protoreflect.FieldNumber(4), field.Number())
			require.Equal(t, protoreflect.StringKind, field.Kind())
			// proto3 `optional`: explicit presence so an omitted ID differs from an empty one.
			require.True(t, field.HasPresence())
			require.True(t, field.HasOptionalKeyword())
		})
	}
}

func TestAttachmentRequestExistingFieldNumbersUnchanged(t *testing.T) {
	t.Parallel()
	for _, msg := range attachmentIDMessages() {
		t.Run(msg.name, func(t *testing.T) {
			for name, number := range msg.existing {
				field := msg.desc.Fields().ByName(name)
				require.NotNil(t, field, name)
				require.Equal(t, number, field.Number(), name)
				require.False(t, field.HasOptionalKeyword(), name)
			}
			require.Equal(t, len(msg.existing)+1, msg.desc.Fields().Len())
		})
	}
}

func TestAttachmentRequestIDHasPresenceOnTheWire(t *testing.T) {
	t.Parallel()
	for _, msg := range attachmentIDMessages() {
		t.Run(msg.name, func(t *testing.T) {
			idField := msg.desc.Fields().ByName("id")
			require.NotNil(t, idField)

			// A client that omits the ID is indistinguishable from an old client.
			require.False(t, msg.new().ProtoReflect().Has(idField))

			set := msg.new()
			set.ProtoReflect().Set(idField, protoreflect.ValueOfString("6f1c1d0a-0000-4000-8000-000000000001"))
			data, err := proto.Marshal(set)
			require.NoError(t, err)
			decoded := msg.new()
			require.NoError(t, proto.Unmarshal(data, decoded))
			require.Equal(t, "6f1c1d0a-0000-4000-8000-000000000001", decoded.ProtoReflect().Get(idField).String())

			// A present-but-empty ID survives the wire so the server can reject it as invalid.
			blank := msg.new()
			blank.ProtoReflect().Set(idField, protoreflect.ValueOfString(""))
			data, err = proto.Marshal(blank)
			require.NoError(t, err)
			decodedBlank := msg.new()
			require.NoError(t, proto.Unmarshal(data, decodedBlank))
			require.True(t, decodedBlank.ProtoReflect().Has(idField))
		})
	}
}

// A message from an old client (no field 4) must decode with the ID absent.
func TestAttachmentRequestFromOldClientHasNoID(t *testing.T) {
	t.Parallel()
	oldUpload, err := proto.Marshal(&pb.UploadMetadata{Filename: "a.txt", ContentType: "text/plain", ExpiresIn: "PT1H"})
	require.NoError(t, err)
	decoded := &pb.UploadMetadata{}
	require.NoError(t, proto.Unmarshal(oldUpload, decoded))
	uploadID := decoded.ProtoReflect().Descriptor().Fields().ByName("id")
	require.NotNil(t, uploadID)
	require.False(t, decoded.ProtoReflect().Has(uploadID))

	oldURL, err := proto.Marshal(&pb.CreateAttachmentFromUrlRequest{SourceUrl: "https://example.com/a", ContentType: "text/plain", Filename: "a"})
	require.NoError(t, err)
	decodedURL := &pb.CreateAttachmentFromUrlRequest{}
	require.NoError(t, proto.Unmarshal(oldURL, decodedURL))
	urlID := decodedURL.ProtoReflect().Descriptor().Fields().ByName("id")
	require.NotNil(t, urlID)
	require.False(t, decodedURL.ProtoReflect().Has(urlID))
}
