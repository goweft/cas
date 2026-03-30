"""Tests for LLM model routing."""
import os
import pytest

from cas.llm import model_for, _DEFAULT_MODELS


class TestModelRouting:

    def test_document_uses_general_model(self):
        assert model_for("document") == "qwen3.5:9b"

    def test_list_uses_general_model(self):
        assert model_for("list") == "qwen3.5:9b"

    def test_code_uses_coder_model(self):
        assert model_for("code") == "qwen2.5-coder:7b"

    def test_chat_uses_general_model(self):
        assert model_for("chat") == "qwen3.5:9b"

    def test_code_model_differs_from_document_model(self):
        assert model_for("code") != model_for("document")

    def test_unknown_type_falls_back_to_document_model(self):
        assert model_for("unknown") == _DEFAULT_MODELS["document"]

    def test_env_override_applies(self, monkeypatch):
        monkeypatch.setenv("CAS_MODEL_CODE", "qwen3.5:27b")
        # Reimport to pick up env change
        import importlib
        import cas.llm
        importlib.reload(cas.llm)
        from cas.llm import model_for as mf
        assert mf("code") == "qwen3.5:27b"
        # Restore
        importlib.reload(cas.llm)

    def test_all_types_have_defaults(self):
        for t in ("document", "code", "list", "chat"):
            m = model_for(t)
            assert m, f"No model configured for type '{t}'"
            assert ":" in m, f"Model '{m}' should include tag (e.g. :9b)"
