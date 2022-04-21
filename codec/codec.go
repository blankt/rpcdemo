package codec

import "io"

//请求头
type Header struct {
	ServiceMethod string //服务名和方法名
	Seq           uint64 //请求序列号或请求id
	Error         string //错误信息
}

//对消息编码的接口，以便实现一个不同的编码方式
type Codec interface {
	io.Closer //用于包装基本的关闭方法
	ReadHeader(*Header) error
	ReadBody(interface{}) error
	Write(*Header, interface{}) error
}

type NewCodecFunc func(io.ReadWriteCloser) Codec

//定义消息序列化类型
type Type string

const (
	GobType  Type = "application/gob"
	JsonType Type = "application/json" // not implemented
)

var NewCodecFuncMap map[Type]NewCodecFunc

//初始化服务器中消息序列化类型
func init() {
	NewCodecFuncMap = make(map[Type]NewCodecFunc)
	NewCodecFuncMap[GobType] = NewGobCodec
}
