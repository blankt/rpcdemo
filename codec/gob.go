package codec

import (
	"bufio"
	"encoding/gob"
	"io"
	"log"
)

type GobCodec struct {
	conn io.ReadWriteCloser //链接实例  通常是通过TCP或者Unix建立的socket连接实例
	buf  *bufio.Writer      //缓冲writer
	dec  *gob.Decoder       //管理从远端读取的类型和数据信息的解释
	enc  *gob.Encoder       //管理将数据和类型信息编码后发送到远端的操作
}

var _ Codec = (*GobCodec)(nil) //确保接口被实现的常用方式，即利用强制类型转换，确保CobCodec实现了Codec接口，这样idea和编译期间就可以检查而不是等到使用的时候

func NewGobCodec(conn io.ReadWriteCloser) Codec {
	buf := bufio.NewWriter(conn)
	return &GobCodec{
		conn: conn,
		buf:  buf,
		dec:  gob.NewDecoder(conn),
		enc:  gob.NewEncoder(buf),
	}
}

//读取请求header
func (c *GobCodec) ReadHeader(h *Header) error {
	return c.dec.Decode(h)
}

//读取请求body
func (c *GobCodec) ReadBody(body interface{}) error {
	return c.dec.Decode(body)
}

//向返回中写入header和body
func (c *GobCodec) Write(h *Header, body interface{}) (err error) {
	defer func() {
		_ = c.buf.Flush()
		if err != nil {
			_ = c.Close()
		}
	}()
	if err = c.enc.Encode(h); err != nil {
		log.Println("rpc: gob error encoding header:", err)
		return
	}
	if err = c.enc.Encode(body); err != nil {
		log.Println("rpc: gob error encoding body:", err)
		return
	}
	return
}

//关闭连接
func (c *GobCodec) Close() error {
	return c.conn.Close()
}
