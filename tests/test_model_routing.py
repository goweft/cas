"""Tests for LLM model routing and multi-provider dispatch."""
import importlib
import os
import pytest


def reload_llm(env: dict = {}):
    """Reload cas.llm with a clean env overlay."""
    import cas.llm
    importlib.reload(cas.llm)
    from cas import llm
    return llm


class TestOllamaModelRouting:
    """Default provider (Ollama) routing."""

    def test_document_uses_general_model(self):
        from cas.llm import model_for
        assert model_for("document") == "qwen3.5:9b"

    def test_list_uses_general_model(self):
        from cas.llm import model_for
        assert model_for("list") == "qwen3.5:9b"

    def test_code_uses_coder_model(self):
        from cas.llm import model_for
        assert model_for("code") == "qwen2.5-coder:7b"

    def test_chat_uses_general_model(self):
        from cas.llm import model_for
        assert model_for("chat") == "qwen3.5:9b"

    def test_code_model_differs_from_document_model(self):
        from cas.llm import model_for
        assert model_for("code") != model_for("document")

    def test_unknown_type_falls_back_to_document_model(self):
        from cas.llm import model_for, _DEFAULT_MODELS, CAS_PROVIDER
        expected = _DEFAULT_MODELS.get(CAS_PROVIDER, _DEFAULT_MODELS["ollama"])["document"]
        assert model_for("unknown") == expected

    def test_all_types_have_defaults(self):
        from cas.llm import model_for
        for t in ("document", "code", "list", "chat"):
            m = model_for(t)
            assert m, f"No model configured for type '{t}'"

    def test_env_override_applies(self, monkeypatch):
        monkeypatch.setenv("CAS_MODEL_CODE", "qwen3.5:27b")
        import cas.llm
        importlib.reload(cas.llm)
        from cas.llm import model_for as mf
        assert mf("code") == "qwen3.5:27b"
        monkeypatch.delenv("CAS_MODEL_CODE", raising=False)
        importlib.reload(cas.llm)


class TestAnthropicModelRouting:
    """Anthropic provider routing."""

    def test_document_uses_sonnet(self, monkeypatch):
        monkeypatch.setenv("CAS_PROVIDER", "anthropic")
        import cas.llm
        importlib.reload(cas.llm)
        from cas.llm import model_for
        assert model_for("document") == "claude-sonnet-4-6"

    def test_code_uses_haiku(self, monkeypatch):
        monkeypatch.setenv("CAS_PROVIDER", "anthropic")
        import cas.llm
        importlib.reload(cas.llm)
        from cas.llm import model_for
        assert model_for("code") == "claude-haiku-4-5-20251001"

    def test_chat_uses_sonnet(self, monkeypatch):
        monkeypatch.setenv("CAS_PROVIDER", "anthropic")
        import cas.llm
        importlib.reload(cas.llm)
        from cas.llm import model_for
        assert model_for("chat") == "claude-sonnet-4-6"

    def test_all_anthropic_types_have_defaults(self, monkeypatch):
        monkeypatch.setenv("CAS_PROVIDER", "anthropic")
        import cas.llm
        importlib.reload(cas.llm)
        from cas.llm import model_for
        for t in ("document", "code", "list", "chat"):
            m = model_for(t)
            assert m, f"No Anthropic model for type '{t}'"
            assert m.startswith("claude-"), f"Expected claude model, got: {m}"

    def teardown_method(self, method):
        # Always reload back to clean state after Anthropic tests
        os.environ.pop("CAS_PROVIDER", None)
        import cas.llm
        importlib.reload(cas.llm)


class TestProviderDispatch:
    """_chat and stream_chat dispatch to the right backend."""

    def test_ollama_provider_is_default(self):
        from cas.llm import CAS_PROVIDER
        assert CAS_PROVIDER == "ollama"

    def test_unknown_provider_falls_back_to_ollama_models(self, monkeypatch):
        monkeypatch.setenv("CAS_PROVIDER", "nonexistent")
        import cas.llm
        importlib.reload(cas.llm)
        from cas.llm import model_for
        # Should not raise, should return something
        m = model_for("document")
        assert m
        monkeypatch.delenv("CAS_PROVIDER", raising=False)
        importlib.reload(cas.llm)

    def test_split_system_extracts_system_message(self):
        from cas.llm import _split_system
        messages = [
            {"role": "system", "content": "You are helpful."},
            {"role": "user",   "content": "Hello"},
        ]
        system, rest = _split_system(messages)
        assert system == "You are helpful."
        assert rest == [{"role": "user", "content": "Hello"}]

    def test_split_system_handles_no_system(self):
        from cas.llm import _split_system
        messages = [{"role": "user", "content": "Hello"}]
        system, rest = _split_system(messages)
        assert system == ""
        assert rest == messages

    def test_anthropic_headers_raises_without_key(self, monkeypatch):
        monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
        from cas.llm import _anthropic_headers
        with pytest.raises(RuntimeError, match="ANTHROPIC_API_KEY"):
            _anthropic_headers()

    def test_anthropic_headers_includes_key(self, monkeypatch):
        monkeypatch.setenv("ANTHROPIC_API_KEY", "sk-ant-test")
        from cas.llm import _anthropic_headers
        headers = _anthropic_headers()
        assert headers["x-api-key"] == "sk-ant-test"
        assert "anthropic-version" in headers
