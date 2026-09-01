// Oracle-only bridge for the public V8 CompiledWasmModule::Serialize method
// omitted by rusty_v8 152.2.0. These declarations mirror v8-wasm.h exactly
// for the two types used at this boundary.

#include <cstddef>
#include <cstdint>
#include <cstdlib>
#include <cstring>
#include <memory>

namespace v8 {

struct OwnedBuffer {
  std::unique_ptr<const uint8_t[]> buffer;
  size_t size = 0;
  OwnedBuffer(std::unique_ptr<const uint8_t[]> buffer, size_t size)
      : buffer(std::move(buffer)), size(size) {}
  OwnedBuffer() = default;
};

class CompiledWasmModule {
 public:
  OwnedBuffer Serialize();
};

}  // namespace v8

extern "C" bool gov8_oracle_compiled_wasm_module_serialize(
    v8::CompiledWasmModule* module, uint8_t** output, size_t* output_size) {
  if (module == nullptr || output == nullptr || output_size == nullptr) {
    return false;
  }
  v8::OwnedBuffer serialized = module->Serialize();
  if (serialized.buffer == nullptr || serialized.size == 0) {
    *output = nullptr;
    *output_size = 0;
    return false;
  }
  auto* copy = static_cast<uint8_t*>(std::malloc(serialized.size));
  if (copy == nullptr) {
    *output = nullptr;
    *output_size = 0;
    return false;
  }
  std::memcpy(copy, serialized.buffer.get(), serialized.size);
  *output = copy;
  *output_size = serialized.size;
  return true;
}

extern "C" void gov8_oracle_serialized_wasm_module_free(uint8_t* bytes) {
  std::free(bytes);
}
