// Code generated by protoc-gen-go. DO NOT EDIT.
// source: xds/core/v3/context_params.proto

package xds_core_v3

import (
	fmt "fmt"
	_ "github.com/cncf/xds/go/udpa/annotations"
	proto "github.com/golang/protobuf/proto"
	math "math"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion3 // please upgrade the proto package

type ContextParams struct {
	Params               map[string]string `protobuf:"bytes,1,rep,name=params,proto3" json:"params,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	XXX_NoUnkeyedLiteral struct{}          `json:"-"`
	XXX_unrecognized     []byte            `json:"-"`
	XXX_sizecache        int32             `json:"-"`
}

func (m *ContextParams) Reset()         { *m = ContextParams{} }
func (m *ContextParams) String() string { return proto.CompactTextString(m) }
func (*ContextParams) ProtoMessage()    {}
func (*ContextParams) Descriptor() ([]byte, []int) {
	return fileDescriptor_a77d5b5f2f15aa7c, []int{0}
}

func (m *ContextParams) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ContextParams.Unmarshal(m, b)
}
func (m *ContextParams) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ContextParams.Marshal(b, m, deterministic)
}
func (m *ContextParams) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ContextParams.Merge(m, src)
}
func (m *ContextParams) XXX_Size() int {
	return xxx_messageInfo_ContextParams.Size(m)
}
func (m *ContextParams) XXX_DiscardUnknown() {
	xxx_messageInfo_ContextParams.DiscardUnknown(m)
}

var xxx_messageInfo_ContextParams proto.InternalMessageInfo

func (m *ContextParams) GetParams() map[string]string {
	if m != nil {
		return m.Params
	}
	return nil
}

func init() {
	proto.RegisterType((*ContextParams)(nil), "xds.core.v3.ContextParams")
	proto.RegisterMapType((map[string]string)(nil), "xds.core.v3.ContextParams.ParamsEntry")
}

func init() { proto.RegisterFile("xds/core/v3/context_params.proto", fileDescriptor_a77d5b5f2f15aa7c) }

var fileDescriptor_a77d5b5f2f15aa7c = []byte{
	// 221 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0xe2, 0x52, 0xa8, 0x48, 0x29, 0xd6,
	0x4f, 0xce, 0x2f, 0x4a, 0xd5, 0x2f, 0x33, 0xd6, 0x4f, 0xce, 0xcf, 0x2b, 0x49, 0xad, 0x28, 0x89,
	0x2f, 0x48, 0x2c, 0x4a, 0xcc, 0x2d, 0xd6, 0x2b, 0x28, 0xca, 0x2f, 0xc9, 0x17, 0xe2, 0xae, 0x48,
	0x29, 0xd6, 0x03, 0xa9, 0xd0, 0x2b, 0x33, 0x96, 0x92, 0x2d, 0x4d, 0x29, 0x48, 0xd4, 0x4f, 0xcc,
	0xcb, 0xcb, 0x2f, 0x49, 0x2c, 0xc9, 0xcc, 0xcf, 0x2b, 0xd6, 0x2f, 0x2e, 0x49, 0x2c, 0x29, 0x85,
	0xaa, 0x55, 0xea, 0x62, 0xe4, 0xe2, 0x75, 0x86, 0x18, 0x12, 0x00, 0x36, 0x43, 0xc8, 0x8e, 0x8b,
	0x0d, 0x62, 0x9a, 0x04, 0xa3, 0x02, 0xb3, 0x06, 0xb7, 0x91, 0x9a, 0x1e, 0x92, 0x71, 0x7a, 0x28,
	0x6a, 0xf5, 0x20, 0x94, 0x6b, 0x5e, 0x49, 0x51, 0x65, 0x10, 0x54, 0x97, 0x94, 0x25, 0x17, 0x37,
	0x92, 0xb0, 0x90, 0x00, 0x17, 0x73, 0x76, 0x6a, 0xa5, 0x04, 0xa3, 0x02, 0xa3, 0x06, 0x67, 0x10,
	0x88, 0x29, 0x24, 0xc2, 0xc5, 0x5a, 0x96, 0x98, 0x53, 0x9a, 0x2a, 0xc1, 0x04, 0x16, 0x83, 0x70,
	0xac, 0x98, 0x2c, 0x18, 0x9d, 0xac, 0x77, 0x35, 0x9c, 0xb8, 0xc8, 0xc6, 0xc4, 0xc1, 0xc8, 0x25,
	0x9d, 0x9c, 0x9f, 0xab, 0x97, 0x9e, 0x59, 0x92, 0x51, 0x9a, 0xa4, 0x07, 0xf2, 0x00, 0xb2, 0x1b,
	0x9c, 0x84, 0x50, 0x1c, 0x11, 0x00, 0xf2, 0x47, 0x00, 0x63, 0x12, 0x1b, 0xd8, 0x43, 0xc6, 0x80,
	0x00, 0x00, 0x00, 0xff, 0xff, 0xe6, 0x4e, 0x2e, 0x13, 0x20, 0x01, 0x00, 0x00,
}