// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package duktape

import "unsafe"

type Context struct{}

type Type uint

func New() *Context { return nil }

func (d *Context) Alloc(size int) unsafe.Pointer                                       { return nil }
func (d *Context) AllocRaw(size int) unsafe.Pointer                                    { return nil }
func (d *Context) Base64Decode(index int)                                              {}
func (d *Context) Base64Encode(index int) string                                       { return "" }
func (d *Context) Call(nargs int)                                                      {}
func (d *Context) CallMethod(nargs int)                                                {}
func (d *Context) CallProp(objIndex int, nargs int)                                    {}
func (d *Context) CheckStack(extra int) bool                                           { return false }
func (d *Context) CheckStackTop(top int) bool                                          { return false }
func (d *Context) CheckType(index int, typ int) bool                                   { return false }
func (d *Context) CheckTypeMask(index int, mask uint) bool                             { return false }
func (d *Context) Compact(objIndex int)                                                {}
func (d *Context) Compile(flags uint)                                                  {}
func (d *Context) CompileFile(flags uint, path string)                                 {}
func (d *Context) CompileLstring(flags uint, src string, lenght int)                   {}
func (d *Context) CompileLstringFilename(flags uint, src string, lenght int)           {}
func (d *Context) CompileString(flags uint, src string)                                {}
func (d *Context) CompileStringFilename(flags uint, src string)                        {}
func (d *Context) Concat(count int)                                                    {}
func (d *Context) ConfigBuffer(bufferIdx int, buffer []byte)                           {}
func (d *Context) Copy(fromIndex int, toIndex int)                                     {}
func (d *Context) DefProp(objIndex int, flags uint)                                    {}
func (d *Context) DelProp(objIndex int) bool                                           { return false }
func (d *Context) DelPropIndex(objIndex int, arrIndex uint) bool                       { return false }
func (d *Context) DelPropString(objIndex int, key string) bool                         { return false }
func (d *Context) Destroy()                                                            {}
func (d *Context) DestroyHeap()                                                        {}
func (d *Context) DumpContextStderr()                                                  {}
func (d *Context) DumpContextStdout()                                                  {}
func (d *Context) DumpFunction()                                                       {}
func (d *Context) Dup(fromIndex int)                                                   {}
func (d *Context) DupTop()                                                             {}
func (d *Context) Enum(objIndex int, enumFlags uint)                                   {}
func (d *Context) Equals(index1 int, index2 int) bool                                  { return false }
func (d *Context) Error(errCode int, str string)                                       {}
func (d *Context) ErrorRaw(errCode int, filename string, line int, errMsg string)      {}
func (d *Context) ErrorVa(errCode int, a ...interface{})                               {}
func (d *Context) Errorf(errCode int, format string, a ...interface{})                 {}
func (d *Context) Eval()                                                               {}
func (d *Context) EvalFile(path string)                                                {}
func (d *Context) EvalFileNoresult(path string)                                        {}
func (d *Context) EvalLstring(src string, lenght int)                                  {}
func (d *Context) EvalLstringNoresult(src string, lenght int)                          {}
func (d *Context) EvalNoresult()                                                       {}
func (d *Context) EvalString(src string)                                               {}
func (d *Context) EvalStringNoresult(src string)                                       {}
func (d *Context) Fatal(errCode int, errMsg string)                                    {}
func (d *Context) FlushTimers()                                                        {}
func (d *Context) Gc(flags uint)                                                       {}
func (d *Context) GetBoolean(index int) bool                                           { return false }
func (d *Context) GetBuffer(index int) (rawPtr unsafe.Pointer, outSize uint)           { return nil, 0 }
func (d *Context) GetContext(index int) *Context                                       { return nil }
func (d *Context) GetCurrentMagic() int                                                { return 0 }
func (d *Context) GetErrorCode(index int) int                                          { return 0 }
func (d *Context) GetFinalizer(index int)                                              {}
func (d *Context) GetGlobalString(key string) bool                                     { return false }
func (d *Context) GetHeapptr(index int) unsafe.Pointer                                 { return nil }
func (d *Context) GetInt(index int) int                                                { return 0 }
func (d *Context) GetLength(index int) int                                             { return 0 }
func (d *Context) GetLstring(index int) string                                         { return "" }
func (d *Context) GetMagic(index int) int                                              { return 0 }
func (d *Context) GetNumber(index int) float64                                         { return 0 }
func (d *Context) GetPointer(index int) unsafe.Pointer                                 { return nil }
func (d *Context) GetProp(objIndex int) bool                                           { return false }
func (d *Context) GetPropIndex(objIndex int, arrIndex uint) bool                       { return false }
func (d *Context) GetPropString(objIndex int, key string) bool                         { return false }
func (d *Context) GetPrototype(index int)                                              {}
func (d *Context) GetString(i int) string                                              { return "" }
func (d *Context) GetTop() int                                                         { return 0 }
func (d *Context) GetTopIndex() int                                                    { return 0 }
func (d *Context) GetType(index int) Type                                              { return 0 }
func (d *Context) GetTypeMask(index int) uint                                          { return 0 }
func (d *Context) GetUint(index int) uint                                              { return 0 }
func (d *Context) HasProp(objIndex int) bool                                           { return false }
func (d *Context) HasPropIndex(objIndex int, arrIndex uint) bool                       { return false }
func (d *Context) HasPropString(objIndex int, key string) bool                         { return false }
func (d *Context) HexDecode(index int)                                                 {}
func (d *Context) HexEncode(index int) string                                          { return "" }
func (d *Context) Insert(toIndex int)                                                  {}
func (d *Context) Instanceof(idx1, idx2 int) bool                                      { return false }
func (d *Context) IsArray(index int) bool                                              { return false }
func (d *Context) IsBoolean(index int) bool                                            { return false }
func (d *Context) IsBoundFunction(index int) bool                                      { return false }
func (d *Context) IsBuffer(index int) bool                                             { return false }
func (d *Context) IsCFunction(index int) bool                                          { return false }
func (d *Context) IsCallable(index int) bool                                           { return false }
func (d *Context) IsConstructorCall() bool                                             { return false }
func (d *Context) IsDynamicBuffer(index int) bool                                      { return false }
func (d *Context) IsEcmascriptFunction(index int) bool                                 { return false }
func (d *Context) IsError(index int) bool                                              { return false }
func (d *Context) IsFixedBuffer(index int) bool                                        { return false }
func (d *Context) IsFunction(index int) bool                                           { return false }
func (d *Context) IsLightfunc(index int) bool                                          { return false }
func (d *Context) IsNan(index int) bool                                                { return false }
func (d *Context) IsNull(index int) bool                                               { return false }
func (d *Context) IsNullOrUndefined(index int) bool                                    { return false }
func (d *Context) IsNumber(index int) bool                                             { return false }
func (d *Context) IsObject(index int) bool                                             { return false }
func (d *Context) IsObjectCoercible(index int) bool                                    { return false }
func (d *Context) IsPointer(index int) bool                                            { return false }
func (d *Context) IsPrimitive(index int) bool                                          { return false }
func (d *Context) IsStrictCall() bool                                                  { return false }
func (d *Context) IsString(index int) bool                                             { return false }
func (d *Context) IsThread(index int) bool                                             { return false }
func (d *Context) IsUndefined(index int) bool                                          { return false }
func (d *Context) IsValidIndex(index int) bool                                         { return false }
func (d *Context) Join(count int)                                                      {}
func (d *Context) JsonDecode(index int)                                                {}
func (d *Context) JsonEncode(index int) string                                         { return "" }
func (d *Context) LoadFunction()                                                       {}
func (d *Context) Log(loglevel int, format string, value interface{})                  {}
func (d *Context) LogVa(logLevel int, format string, values ...interface{})            {}
func (d *Context) Must() *Context                                                      { return nil }
func (d *Context) New(nargs int)                                                       {}
func (d *Context) Next(enumIndex int, getValue bool) bool                              { return false }
func (d *Context) NormalizeIndex(index int) int                                        { return 0 }
func (d *Context) Pcall(nargs int) int                                                 { return 0 }
func (d *Context) PcallMethod(nargs int) int                                           { return 0 }
func (d *Context) PcallProp(objIndex int, nargs int) int                               { return 0 }
func (d *Context) Pcompile(flags uint) error                                           { return nil }
func (d *Context) PcompileFile(flags uint, path string) error                          { return nil }
func (d *Context) PcompileLstring(flags uint, src string, lenght int) error            { return nil }
func (d *Context) PcompileLstringFilename(flags uint, src string, lenght int) error    { return nil }
func (d *Context) PcompileString(flags uint, src string) error                         { return nil }
func (d *Context) PcompileStringFilename(flags uint, src string) error                 { return nil }
func (d *Context) Peval() error                                                        { return nil }
func (d *Context) PevalFile(path string) error                                         { return nil }
func (d *Context) PevalFileNoresult(path string) int                                   { return 0 }
func (d *Context) PevalLstring(src string, lenght int) error                           { return nil }
func (d *Context) PevalLstringNoresult(src string, lenght int) int                     { return 0 }
func (d *Context) PevalNoresult() int                                                  { return 0 }
func (d *Context) PevalString(src string) error                                        { return nil }
func (d *Context) PevalStringNoresult(src string) int                                  { return 0 }
func (d *Context) Pnew(nargs int) error                                                { return nil }
func (d *Context) Pop()                                                                {}
func (d *Context) Pop2()                                                               {}
func (d *Context) Pop3()                                                               {}
func (d *Context) PopN(count int)                                                      {}
func (d *Context) PushArray() int                                                      { return 0 }
func (d *Context) PushBoolean(val bool)                                                {}
func (d *Context) PushBuffer(size int, dynamic bool) unsafe.Pointer                    { return nil }
func (d *Context) PushBufferObject(bufferIdx, size, length int, flags uint)            {}
func (d *Context) PushCFunction(fn *[0]byte, nargs int64) int                          { return 0 }
func (d *Context) PushCLightfunc(fn *[0]byte, nargs, length, magic int) int            { return 0 }
func (d *Context) PushContextDump()                                                    {}
func (d *Context) PushCurrentFunction()                                                {}
func (d *Context) PushCurrentThread()                                                  {}
func (d *Context) PushDynamicBuffer(size int) unsafe.Pointer                           { return nil }
func (d *Context) PushErrorObject(errCode int, format string, value interface{})       {}
func (d *Context) PushErrorObjectVa(errCode int, format string, values ...interface{}) {}
func (d *Context) PushExternalBuffer()                                                 {}
func (d *Context) PushFalse()                                                          {}
func (d *Context) PushFixedBuffer(size int) unsafe.Pointer                             { return nil }
func (d *Context) PushGlobalGoFunction(name string, fn func(*Context) int) (int, error) {
	return 0, nil
}
func (d *Context) PushGlobalObject()                                               {}
func (d *Context) PushGlobalStash()                                                {}
func (d *Context) PushGoFunction(fn func(*Context) int) int                        { return 0 }
func (d *Context) PushHeapStash()                                                  {}
func (d *Context) PushHeapptr(ptr unsafe.Pointer)                                  {}
func (d *Context) PushInt(val int)                                                 {}
func (d *Context) PushLstring(str string, lenght int) string                       { return "" }
func (d *Context) PushNan()                                                        {}
func (d *Context) PushNull()                                                       {}
func (d *Context) PushNumber(val float64)                                          {}
func (d *Context) PushObject() int                                                 { return 0 }
func (d *Context) PushPointer(p unsafe.Pointer)                                    {}
func (d *Context) PushString(str string) string                                    { return "" }
func (d *Context) PushStringFile(path string) string                               { return "" }
func (d *Context) PushThis()                                                       {}
func (d *Context) PushThread() int                                                 { return 0 }
func (d *Context) PushThreadNewGlobalenv() int                                     { return 0 }
func (d *Context) PushThreadStash(targetCtx *Context)                              {}
func (d *Context) PushTimers() error                                               { return nil }
func (d *Context) PushTrue()                                                       {}
func (d *Context) PushUint(val uint)                                               {}
func (d *Context) PushUndefined()                                                  {}
func (d *Context) PutGlobalString(key string) bool                                 { return false }
func (d *Context) PutProp(objIndex int) bool                                       { return false }
func (d *Context) PutPropIndex(objIndex int, arrIndex uint) bool                   { return false }
func (d *Context) PutPropString(objIndex int, key string) bool                     { return false }
func (d *Context) Remove(index int)                                                {}
func (d *Context) Replace(toIndex int)                                             {}
func (d *Context) RequireBoolean(index int) bool                                   { return false }
func (d *Context) RequireBuffer(index int) (rawPtr unsafe.Pointer, outSize uint)   { return nil, 0 }
func (d *Context) RequireCallable(index int)                                       {}
func (d *Context) RequireContext(index int) *Context                               { return nil }
func (d *Context) RequireFunction(index int)                                       {}
func (d *Context) RequireHeapptr(index int) unsafe.Pointer                         { return nil }
func (d *Context) RequireInt(index int) int                                        { return 0 }
func (d *Context) RequireLstring(index int) string                                 { return "" }
func (d *Context) RequireNormalizeIndex(index int) int                             { return 0 }
func (d *Context) RequireNull(index int)                                           {}
func (d *Context) RequireNumber(index int) float64                                 { return 0 }
func (d *Context) RequireObjectCoercible(index int)                                {}
func (d *Context) RequirePointer(index int) unsafe.Pointer                         { return nil }
func (d *Context) RequireStack(extra int)                                          {}
func (d *Context) RequireStackTop(top int)                                         {}
func (d *Context) RequireString(index int) string                                  { return "" }
func (d *Context) RequireTopIndex() int                                            { return 0 }
func (d *Context) RequireTypeMask(index int, mask uint)                            {}
func (d *Context) RequireUint(index int) uint                                      { return 0 }
func (d *Context) RequireUndefined(index int)                                      {}
func (d *Context) RequireValidIndex(index int)                                     {}
func (d *Context) ResizeBuffer(index int, newSize int) unsafe.Pointer              { return nil }
func (d *Context) SafeCall(fn, args *[0]byte, nargs, nrets int) int                { return 0 }
func (d *Context) SafeToLstring(index int) string                                  { return "" }
func (d *Context) SafeToString(index int) string                                   { return "" }
func (d *Context) SetFinalizer(index int)                                          {}
func (d *Context) SetGlobalObject()                                                {}
func (d *Context) SetMagic(index int, magic int)                                   {}
func (d *Context) SetPrototype(index int)                                          {}
func (d *Context) SetTop(index int)                                                {}
func (d *Context) StrictEquals(index1 int, index2 int) bool                        { return false }
func (d *Context) Substring(index int, startCharOffset int, endCharOffset int)     {}
func (d *Context) Swap(index1 int, index2 int)                                     {}
func (d *Context) SwapTop(index int)                                               {}
func (d *Context) Throw()                                                          {}
func (d *Context) ToBoolean(index int) bool                                        { return false }
func (d *Context) ToBuffer(index int) (rawPtr unsafe.Pointer, outSize uint)        { return nil, 0 }
func (d *Context) ToDefaultvalue(index int, hint int)                              {}
func (d *Context) ToDynamicBuffer(index int) (rawPtr unsafe.Pointer, outSize uint) { return nil, 0 }
func (d *Context) ToFixedBuffer(index int) (rawPtr unsafe.Pointer, outSize uint)   { return nil, 0 }
func (d *Context) ToInt(index int) int                                             { return 0 }
func (d *Context) ToInt32(index int) int32                                         { return 0 }
func (d *Context) ToLstring(index int) string                                      { return "" }
func (d *Context) ToNull(index int)                                                {}
func (d *Context) ToNumber(index int) float64                                      { return 0 }
func (d *Context) ToObject(index int)                                              {}
func (d *Context) ToPointer(index int) unsafe.Pointer                              { return nil }
func (d *Context) ToPrimitive(index int, hint int)                                 {}
func (d *Context) ToString(index int) string                                       { return "" }
func (d *Context) ToUint(index int) uint                                           { return 0 }
func (d *Context) ToUint16(index int) uint16                                       { return 0 }
func (d *Context) ToUint32(index int) uint32                                       { return 0 }
func (d *Context) ToUndefined(index int)                                           {}
func (d *Context) Trim(index int)                                                  {}
func (d *Context) XcopyTop(fromCtx *Context, count int)                            {}
func (d *Context) XmoveTop(fromCtx *Context, count int)                            {}
