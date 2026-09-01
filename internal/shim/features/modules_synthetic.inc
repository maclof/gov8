// modules_synthetic.inc -- production SyntheticModule bindings.
// Included after modules.inc so ModuleWrap and the core scope wrappers exist.

#include <memory>
#include <mutex>
#include <unordered_set>
#include <vector>

namespace gov8_synthetic_impl {

using GoSyntheticEvaluationDispatcher =
    int64_t (*)(uint64_t, void*, void*, void**);

GoSyntheticEvaluationDispatcher synthetic_dispatcher = nullptr;

extern "C" {
const v8::Module* v8__Module__CreateSyntheticModule(
    v8::Isolate* isolate, const v8::String* module_name,
    size_t export_names_len, const v8::String* export_names_raw[],
    v8::Module::SyntheticModuleEvaluationSteps evaluation_steps);
const v8::Value* v8__Module__Evaluate(const v8::Module& self,
                                      const v8::Context& context);
}

template <class T>
T& SyntheticDeref(const v8::Local<T>& local) {
  return *local.operator->();
}

struct SyntheticCallbackBinding {
  v8::Isolate* isolate;
  uint64_t callback_id;
  std::unique_ptr<v8::Global<v8::Module>> module;
};

std::mutex synthetic_binding_mutex;
std::vector<SyntheticCallbackBinding> synthetic_bindings;

uint64_t FindSyntheticCallback(v8::Isolate* isolate,
                               v8::Local<v8::Module> module) {
  std::lock_guard<std::mutex> lock(synthetic_binding_mutex);
  for (const auto& binding : synthetic_bindings) {
    if (binding.isolate == isolate &&
        binding.module->Get(isolate) == module) {
      return binding.callback_id;
    }
  }
  return 0;
}

v8::MaybeLocal<v8::Value> SyntheticEvaluationSteps(
    v8::Local<v8::Context> context, v8::Local<v8::Module> module) {
  v8::Isolate* isolate = v8::Isolate::GetCurrent();
  if (synthetic_dispatcher == nullptr || !module->IsSyntheticModule()) {
    return v8::MaybeLocal<v8::Value>();
  }
  const uint64_t callback_id = FindSyntheticCallback(isolate, module);
  if (callback_id == 0) return v8::MaybeLocal<v8::Value>();

  GoScope callback_scope{};
  callback_scope.magic = kScopeMagic;
  callback_scope.iso = isolate;
  GlobalPlacementNew<v8::HandleScope>(callback_scope.hs_storage, isolate);
  void* result_wire = nullptr;
  const int64_t handled = synthetic_dispatcher(
      callback_id, ToWire(context), &callback_scope, &result_wire);
  v8::Global<v8::Value> result;
  if (handled == 1 && result_wire != nullptr) {
    result.Reset(isolate, FromWire<v8::Value>(result_wire));
  }
  callback_scope.hs()->~HandleScope();
  callback_scope.magic = 0;
  if (handled != 1) return v8::MaybeLocal<v8::Value>();
  if (result.IsEmpty()) return v8::Undefined(isolate);
  v8::Local<v8::Value> local = result.Get(isolate);
  result.Reset();
  return local;
}

}  // namespace gov8_synthetic_impl

extern "C" {

using namespace gov8_synthetic_impl;

__declspec(dllexport) int64_t gov8_synthetic_set_dispatcher(void* dispatcher) {
  ClearErr();
  if (dispatcher == nullptr) {
    SetErr("null synthetic module dispatcher");
    return kErrBadArg;
  }
  synthetic_dispatcher =
      reinterpret_cast<GoSyntheticEvaluationDispatcher>(dispatcher);
  return kOk;
}

__declspec(dllexport) int64_t gov8_synthetic_create(
    v8::Isolate* iso, void* ctxw, void* scope, const char* module_name,
    int64_t module_name_len, const char* const* export_names,
    const int64_t* export_name_lengths, int64_t export_count,
    uint64_t callback_id, void** out_module) {
  ClearErr();
  CtxWrap* context = AsCtx(ctxw);
  if (context == nullptr || out_module == nullptr || callback_id == 0 ||
      !ScopeIs(iso, scope) || !OwnedBy(iso, context->iso) ||
      (module_name == nullptr && module_name_len > 0) || module_name_len < 0 ||
      module_name_len > INT_MAX || export_count < 0 || export_count > INT_MAX ||
      (export_count > 0 &&
       (export_names == nullptr || export_name_lengths == nullptr))) {
    SetErr("invalid argument");
    return kErrBadArg;
  }
  try {
    Gov8IsolateScope isolate_scope(iso);
    v8::Local<v8::Context> ctx = context->ctx->Get(iso);
    v8::Context::Scope context_scope(ctx);
    v8::Local<v8::String> name;
    const char* module_name_data = module_name == nullptr ? "" : module_name;
    if (!v8::String::NewFromUtf8(iso, module_name_data,
                                 v8::NewStringType::kNormal,
                                 static_cast<int>(module_name_len))
             .ToLocal(&name)) {
      SetErr("module name allocation failed");
      return kErrGeneric;
    }
    std::vector<v8::Local<v8::String>> exports;
    exports.reserve(static_cast<size_t>(export_count));
    std::unordered_set<std::string> unique_names;
    for (int64_t index = 0; index < export_count; ++index) {
      const int64_t length = export_name_lengths[index];
      if (length < 0 || length > INT_MAX ||
          (export_names[index] == nullptr && length > 0)) {
        SetErr("invalid export name");
        return kErrBadArg;
      }
      std::string bytes(export_names[index] == nullptr ? "" : export_names[index],
                        static_cast<size_t>(length));
      if (!unique_names.insert(bytes).second) {
        SetErr("duplicate synthetic module export name");
        return kErrBadArg;
      }
      v8::Local<v8::String> export_name;
      if (!v8::String::NewFromUtf8(iso, bytes.data(),
                                   v8::NewStringType::kNormal,
                                   static_cast<int>(bytes.size()))
               .ToLocal(&export_name)) {
        SetErr("export name allocation failed");
        return kErrGeneric;
      }
      exports.push_back(export_name);
    }
    std::vector<const v8::String*> export_wires;
    export_wires.reserve(exports.size());
    for (v8::Local<v8::String> export_name : exports) {
      export_wires.push_back(
          static_cast<const v8::String*>(ToWire(export_name)));
    }
    const v8::Module* module_wire = v8__Module__CreateSyntheticModule(
        iso, static_cast<const v8::String*>(ToWire(name)),
        export_wires.size(), export_wires.data(), SyntheticEvaluationSteps);
    if (module_wire == nullptr) {
      SetErr("CreateSyntheticModule returned empty");
      return kErrGeneric;
    }
    v8::Local<v8::Module> module = FromWire<v8::Module>(
        const_cast<v8::Module*>(module_wire));
    auto* wrap = new ModuleWrap{};
    wrap->magic = kModuleMagic;
    wrap->iso = iso;
    wrap->ctx = context;
    wrap->module = new v8::Global<v8::Module>(iso, module);
    {
      std::lock_guard<std::mutex> lock(synthetic_binding_mutex);
      synthetic_bindings.push_back(SyntheticCallbackBinding{
          iso, callback_id,
          std::make_unique<v8::Global<v8::Module>>(iso, module)});
    }
    *out_module = wrap;
    return kOk;
  } catch (...) {
    SetErr("C++ exception creating synthetic module");
    return kErrCpp;
  }
}

__declspec(dllexport) int64_t gov8_synthetic_unregister(
    v8::Isolate* iso, uint64_t callback_id) {
  ClearErr();
  if (iso == nullptr || callback_id == 0) {
    SetErr("invalid argument");
    return kErrBadArg;
  }
  std::lock_guard<std::mutex> lock(synthetic_binding_mutex);
  for (auto it = synthetic_bindings.begin(); it != synthetic_bindings.end();
       ++it) {
    if (it->isolate == iso && it->callback_id == callback_id) {
      it->module->Reset();
      synthetic_bindings.erase(it);
      return kOk;
    }
  }
  SetErr("synthetic callback binding not found");
  return kErrState;
}

__declspec(dllexport) int64_t gov8_synthetic_set_export(
    v8::Isolate* iso, void* scope, void* modulew, void* tcw,
    const char* export_name, int64_t export_name_len, void* value_wire,
    int32_t* out) {
  ClearErr();
  ModuleWrap* module = AsModule(modulew);
  if (module == nullptr || out == nullptr || value_wire == nullptr ||
      !ScopeIs(iso, scope) || !OwnedBy(iso, module->iso) ||
      (export_name == nullptr && export_name_len > 0) ||
      export_name_len < 0 || export_name_len > INT_MAX) {
    SetErr("invalid argument");
    return kErrBadArg;
  }
  try {
    Gov8IsolateScope isolate_scope(iso);
    if (!module->module->Get(iso)->IsSyntheticModule()) {
      SetErr("module is not synthetic");
      return kErrState;
    }
    v8::Local<v8::Context> ctx = module->ctx->ctx->Get(iso);
    v8::Context::Scope context_scope(ctx);
    FallbackTryCatch fallback;
    v8::TryCatch* try_catch = nullptr;
    if (tcw != nullptr) {
      TcWrap* wrap = AsTc(tcw);
      if (wrap == nullptr || !OwnedBy(iso, wrap->iso)) {
        SetErr("invalid trycatch handle");
        return kErrBadArg;
      }
      try_catch = TcOf(wrap);
    }
    if (try_catch == nullptr) try_catch = fallback.Init(iso);
    v8::Local<v8::String> name;
    const char* export_name_data = export_name == nullptr ? "" : export_name;
    if (!v8::String::NewFromUtf8(iso, export_name_data,
                                 v8::NewStringType::kNormal,
                                 static_cast<int>(export_name_len))
             .ToLocal(&name)) {
      SetErr("export name allocation failed");
      return kErrGeneric;
    }
    v8::Maybe<bool> result = module->module->Get(iso)->SetSyntheticModuleExport(
        iso, name, FromWire<v8::Value>(value_wire));
    if (result.IsNothing()) {
      SetErr("synthetic module export update failed");
      return kErrException;
    }
    *out = result.FromJust() ? 1 : 0;
    return kOk;
  } catch (...) {
    SetErr("C++ exception updating synthetic module export");
    return kErrCpp;
  }
}

__declspec(dllexport) int64_t gov8_synthetic_evaluate(
    v8::Isolate* iso, void* ctxw, void* scope, void* modulew, void* tcw,
    void** out_value) {
  ClearErr();
  CtxWrap* context = AsCtx(ctxw);
  ModuleWrap* module = AsModule(modulew);
  if (context == nullptr || module == nullptr || out_value == nullptr ||
      !ScopeIs(iso, scope) || !OwnedBy(iso, context->iso) ||
      !OwnedBy(iso, module->iso) || module->ctx != context) {
    SetErr("invalid argument");
    return kErrBadArg;
  }
  try {
    Gov8IsolateScope isolate_scope(iso);
    if (!module->module->Get(iso)->IsSyntheticModule()) {
      SetErr("module is not synthetic");
      return kErrState;
    }
    v8::Local<v8::Context> ctx = context->ctx->Get(iso);
    v8::Context::Scope context_scope(ctx);
    FallbackTryCatch fallback;
    v8::TryCatch* try_catch = nullptr;
    if (tcw != nullptr) {
      TcWrap* wrap = AsTc(tcw);
      if (wrap == nullptr || !OwnedBy(iso, wrap->iso)) {
        SetErr("invalid trycatch handle");
        return kErrBadArg;
      }
      try_catch = TcOf(wrap);
    }
    if (try_catch == nullptr) try_catch = fallback.Init(iso);
    const v8::Value* value = v8__Module__Evaluate(
        SyntheticDeref(module->module->Get(iso)), SyntheticDeref(ctx));
    if (value == nullptr) {
      SetErr("synthetic module evaluate failed");
      return kErrException;
    }
    *out_value = const_cast<v8::Value*>(value);
    return kOk;
  } catch (...) {
    SetErr("C++ exception evaluating synthetic module");
    return kErrCpp;
  }
}

}  // extern "C"
