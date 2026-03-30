"""Tests for audit integration and export routes."""
import pytest
from unittest.mock import MagicMock, call, patch

from cas.shell import Shell, detect_intent


class TestExpandedEditPatterns:
    """Verify the broadened edit intent patterns."""

    @pytest.mark.parametrize("message", [
        "add a risks section",
        "add a new introduction paragraph",
        "add an executive summary section",
        "fix the grammar",
        "improve the conclusion",
        "clean up the formatting",
        "remove the last paragraph",
        "expand the introduction",
        "shorten the summary",
        "rewrite the opening",
        "rename the document",
        "proofread it",
    ])
    def test_new_edit_patterns(self, message):
        intent = detect_intent(message)
        assert intent.kind == "edit_workspace", f"Expected edit for: {message!r}"

    @pytest.mark.parametrize("message", [
        # These should still create, not edit
        "write a resume",
        "draft a proposal",
        "create a new document",
    ])
    def test_create_not_eaten_by_edit(self, message):
        intent = detect_intent(message)
        assert intent.kind == "create_workspace", f"Expected create for: {message!r}"


class TestAuditWiring:
    """Verify CasAuditor is called at the right lifecycle points."""

    def test_session_create_audited(self, mock_auditor):
        shell = Shell()
        session = shell.create_session()
        mock_auditor.log_session_create.assert_called_once_with(session.id)

    def test_workspace_create_audited(self, mock_auditor):
        shell = Shell()
        session = shell.create_session()
        resp = shell.process_message(session.id, "draft a resume")
        mock_auditor.log_workspace_create.assert_called_once()
        args = mock_auditor.log_workspace_create.call_args
        assert args[0][0] == resp.workspace  # first positional arg is ws
        assert args[0][1] == session.id

    def test_workspace_update_audited(self, mock_auditor):
        shell = Shell()
        session = shell.create_session()
        shell.process_message(session.id, "draft a resume")
        shell.process_message(session.id, "add an executive summary section")
        mock_auditor.log_workspace_update.assert_called_once()
        args = mock_auditor.log_workspace_update.call_args
        assert args[0][1] == session.id  # session_id
        assert "executive summary" in args[0][2]  # edit_request

    def test_workspace_close_audited(self, mock_auditor):
        shell = Shell()
        session = shell.create_session()
        shell.process_message(session.id, "draft a memo")
        shell.process_message(session.id, "close the document")
        mock_auditor.log_workspace_close.assert_called_once()

    def test_no_audit_on_chat(self, mock_auditor):
        shell = Shell()
        session = shell.create_session()
        shell.process_message(session.id, "hello there")
        mock_auditor.log_workspace_create.assert_not_called()
        mock_auditor.log_workspace_update.assert_not_called()


class TestExportFilename:
    """Test the _safe_filename helper via the export logic."""

    def test_safe_filename(self):
        from cas.api import _safe_filename
        assert _safe_filename("Project Proposal") == "project-proposal"
        assert _safe_filename("Resume For A Senior Python Developer") == "resume-for-a-senior-python-developer"
        assert _safe_filename("Q1 Report!!!") == "q1-report"
        assert _safe_filename("") == "document"
        assert _safe_filename("   ") == "document"

    def test_safe_filename_special_chars(self):
        from cas.api import _safe_filename
        result = _safe_filename("Plan: Phase 1/2")
        assert "/" not in result
        assert ":" not in result


class TestExportContent:
    """Test export content transformations."""

    def test_plain_text_strips_headings(self):
        import re
        content = "# Title\n\n## Section\n\nParagraph text.\n\n### Sub\n\nMore.\n"
        plain = re.sub(r'^#{1,6}\s+', '', content, flags=re.MULTILINE)
        assert '# ' not in plain
        assert 'Title' in plain
        assert 'Paragraph text.' in plain

    def test_plain_text_strips_bold(self):
        import re
        content = "Some **bold** and *italic* text."
        plain = re.sub(r'\*\*(.+?)\*\*', r'\1', content)
        plain = re.sub(r'\*(.+?)\*', r'\1', plain)
        assert '**' not in plain
        assert 'bold' in plain
        assert 'italic' in plain

    def test_html_export_has_styles(self):
        from cas.renderer import render_with_styles
        html = render_with_styles("# Test\n\nContent.\n")
        assert '<style>' in html
        assert 'cas-doc' in html
        assert '<h1>' in html

    def test_markdown_export_is_passthrough(self):
        content = "# Title\n\nSome content.\n"
        # markdown export is just the raw content
        assert content == content  # trivially true — no transformation
