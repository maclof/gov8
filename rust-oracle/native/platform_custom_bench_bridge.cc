// Benchmark-only producer for a synthetic no-op v8::Task. This intentionally
// mirrors the public v8-platform.h Task ABI without pulling V8 headers into the
// oracle's standalone native bridge build.

#include <atomic>
#include <cstdint>
#include <new>

namespace v8 {

class Task {
 public:
  virtual ~Task() = default;
  virtual void Run() = 0;
};

}  // namespace v8

extern "C" void v8__Platform__CustomPlatform__BASE__PostTask(
    void* context, void* isolate, v8::Task* task);

namespace {

std::atomic<uint64_t> created{0};
std::atomic<uint64_t> run{0};
std::atomic<uint64_t> destroyed{0};

class NoopTask final : public v8::Task {
 public:
  NoopTask() { created.fetch_add(1); }
  ~NoopTask() override { destroyed.fetch_add(1); }
  void Run() override { run.fetch_add(1); }
};

}  // namespace

extern "C" bool gov8_oracle_platform_bench_post_noop_task(void* context,
                                                           void* isolate) {
  auto* task = new (std::nothrow) NoopTask();
  if (task == nullptr) return false;
  v8__Platform__CustomPlatform__BASE__PostTask(context, isolate, task);
  return true;
}

extern "C" void gov8_oracle_platform_bench_reset_noop_task_counts() {
  created.store(0);
  run.store(0);
  destroyed.store(0);
}

extern "C" void gov8_oracle_platform_bench_noop_task_counts(
    uint64_t* created_out, uint64_t* run_out, uint64_t* destroyed_out) {
  *created_out = created.load();
  *run_out = run.load();
  *destroyed_out = destroyed.load();
}
