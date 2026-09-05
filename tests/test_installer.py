"""Installer failure tests run with isolated files and mocked system services/network."""
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]

class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        (self.root / 'bin').mkdir()
        (self.root / 'units').mkdir()
        (self.root / 'config').mkdir()
        (self.root / 'bin/vibemonitor').write_text('old binary')
        (self.root / 'units/vibemonitor-server.service').write_text('old unit')
        fixture = b'#!/bin/sh\necho VibeMonitor-test\n'
        (self.root / 'fixture').write_bytes(fixture)
        (self.root / 'sums').write_text(hashlib.sha256(fixture).hexdigest() + '  vibemonitor-linux-amd64\n')
        self.prelude = r'''
source "$SOURCE_INSTALLER"
INSTALL_BIN="$TEST_ROOT/bin/vibemonitor"
CONFIG_DIR="$TEST_ROOT/config"
UNIT_DIR="$TEST_ROOT/units"
check_root() { :; }
read_input() { printf -v "$2" yes; }
detect_arch() { SYSTEM_ARCH=amd64; }
check_dependencies() { :; }
resolve_release() { RELEASE_BASE=https://example.invalid/v1; }
sleep() { :; }
systemctl() {
    echo "$*" >> "$TEST_ROOT/service-calls"
    if [ "$1" = restart ] && [ "$FAIL_NEW" = 1 ] && [ "$(cat "$INSTALL_BIN")" != 'old binary' ]; then return 1; fi
    return 0
}
# Only the ELF-header probe is mocked for this cross-platform fixture.
od() { case "$*" in *-j18*) echo '3e 00';; *) echo '7f 45 4c 46 02';; esac; }
sha256sum() { shasum -a 256 "$@"; }
curl() {
    local output='' last=''
    while [ $# -gt 0 ]; do
        case "$1" in -o) output="$2"; shift 2;; *) last="$1"; shift;; esac
    done
    if [ -z "$output" ]; then echo pong; return; fi
    case "$last" in */sha256sums.txt) cp "$TEST_ROOT/sums" "$output";; *) cp "$TEST_ROOT/fixture" "$output";; esac
}
'''

    def run_installer(self, code, fail=False):
        return subprocess.run(['bash', '-c', self.prelude + code], capture_output=True, text=True,
            env={**os.environ, 'SOURCE_INSTALLER':str(ROOT / 'install.sh'), 'TEST_ROOT':str(self.root), 'FAIL_NEW':'1' if fail else '0'}, timeout=10)

    def test_cleanup_requires_confirmation(self):
        backups = self.root / 'config/backups'
        backups.mkdir()
        (backups / 'old.json').write_text('backup')
        data = self.root / 'config/vibemonitor-data.json'
        data.write_text('old credentials')
        result = self.run_installer('read_input() { printf -v "$2" no; }\ninstall_server 1314 pass\n')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((backups / 'old.json').exists())
        self.assertTrue(data.exists())
        self.assertEqual((self.root / 'bin/vibemonitor').read_text(), 'old binary')
        result = self.run_installer('install_server 1314 pass\n')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(backups.exists())
        self.assertFalse(data.exists())

    def test_uninstall_cleans_all_data(self):
        backups = self.root / 'config/backups'
        backups.mkdir()
        (backups / 'old.json').write_text('backup')
        data = self.root / 'config/vibemonitor-data.json'
        data.write_text('keep data')
        result = self.run_installer('uninstall_all\n')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(backups.exists())
        self.assertFalse(data.exists())

    def test_successful_atomic_update(self):
        result = self.run_installer('install_server 1314 "password with spaces"\n')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / 'bin/vibemonitor').read_bytes(), (self.root / 'fixture').read_bytes())
        self.assertIn('"password with spaces"', (self.root / 'units/vibemonitor-server.service').read_text())
        self.assertEqual(list((self.root / 'bin').glob('*.update.*')), [])

    def test_restart_failure_restores_binary_and_unit(self):
        result = self.run_installer('install_server 1314 "pass"\n', fail=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / 'bin/vibemonitor').read_text(), 'old binary')
        self.assertEqual((self.root / 'units/vibemonitor-server.service').read_text(), 'old unit')
        self.assertEqual(list((self.root / 'bin').glob('*.update.*')), [])

    def test_failed_rollback_preserves_recovery_files(self):
        overrides = [
            'mv() { case "$*" in *restore-binary*) return 1;; *) command mv "$@";; esac; }',
            'cp() { case "$*" in *"previous-unit "*) return 1;; *) command cp "$@";; esac; }',
            'systemctl() { if [ "$1" = daemon-reload ]; then return 1; fi; return 0; }',
        ]
        for override in overrides:
            with self.subTest(override=override):
                # Each attempt starts from a working version and unit.
                (self.root / 'bin/vibemonitor').write_text('old binary')
                (self.root / 'units/vibemonitor-server.service').write_text('old unit')
                before = set((self.root / 'bin').glob('*.update.*'))
                result = self.run_installer(override + '\ninstall_server 1314 "pass"\n', fail=True)
                self.assertNotEqual(result.returncode, 0)
                retained = set((self.root / 'bin').glob('*.update.*')) - before
                self.assertEqual(len(retained), 1, result.stderr)
                recovery = retained.pop()
                self.assertEqual((recovery / 'previous-binary').read_text(), 'old binary')
                self.assertEqual((recovery / 'previous-unit').read_text(), 'old unit')
                self.assertIn(str(recovery), result.stderr)

    def test_bad_checksum_never_replaces_or_stops_service(self):
        (self.root / 'sums').write_text('0' * 64 + '  vibemonitor-linux-amd64\n')
        result = self.run_installer('install_server 1314 "pass"\n')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / 'bin/vibemonitor').read_text(), 'old binary')
        self.assertNotIn('stop vibemonitor-server', (self.root / 'service-calls').read_text())

    def test_html_is_rejected_even_with_matching_checksum(self):
        result = self.run_installer('od() { echo "3c 68 74 6d 6c"; }; install_server 1314 "pass"\n')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / 'bin/vibemonitor').read_text(), 'old binary')

    def test_pipe_without_terminal_fails_with_usage(self):
        result = subprocess.run(['bash'], input=(ROOT / 'install.sh').read_text(), capture_output=True,
            text=True, start_new_session=True, timeout=10)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('No interactive terminal', result.stderr)

    def test_restore_keeps_a_backup_and_replaces_data(self):
        (self.root / 'config/vibemonitor-data.json').write_text('old data')
        (self.root / 'restored').write_text('restored data')
        (self.root / 'restored.ping.json').write_text('restored ping')
        (self.root / 'config/vibemonitor-data.json.ping.json').write_text('old ping')
        binary = self.root / 'bin/vibemonitor'
        binary.write_text('#!/bin/sh\nexit 0\n')
        binary.chmod(0o755)
        result = self.run_installer('restore_data "$TEST_ROOT/restored"\n')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / 'config/vibemonitor-data.json').read_text(), 'restored data')
        self.assertEqual((self.root / 'config/vibemonitor-data.json.ping.json').read_text(), 'restored ping')
        self.assertTrue(any(p.read_text() == 'old ping' for p in (self.root / 'config/backups').iterdir()))
        self.assertTrue(any(p.read_text() == 'old data' for p in (self.root / 'config/backups').iterdir()))

    def test_failed_restore_start_rolls_data_back(self):
        (self.root / 'config/vibemonitor-data.json').write_text('old data')
        (self.root / 'restored').write_text('restored data')
        (self.root / 'restored.ping.json').write_text('restored ping')
        (self.root / 'config/vibemonitor-data.json.ping.json').write_text('old ping')
        binary = self.root / 'bin/vibemonitor'
        binary.write_text('#!/bin/sh\nexit 0\n')
        binary.chmod(0o755)
        result = self.run_installer(r"""
systemctl() {
    if [ "$1" = start ] && [ "$(cat "$CONFIG_DIR/vibemonitor-data.json")" = 'restored data' ]; then return 1; fi
    return 0
}
restore_data "$TEST_ROOT/restored"
""")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / 'config/vibemonitor-data.json').read_text(), 'old data')
        self.assertEqual((self.root / 'config/vibemonitor-data.json.ping.json').read_text(), 'old ping')

    def test_invalid_backup_does_not_stop_service(self):
        (self.root / 'config/vibemonitor-data.json').write_text('old data')
        binary = self.root / 'bin/vibemonitor'
        binary.write_text('#!/bin/sh\nexit 1\n')
        binary.chmod(0o755)
        result = self.run_installer('restore_data "$TEST_ROOT/missing"\n')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / 'config/vibemonitor-data.json').read_text(), 'old data')
        self.assertFalse((self.root / 'service-calls').exists())

    def test_unit_arguments_escape_expansion(self):
        result = self.run_installer("unit_arg 'a$b%c\"d'\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, '"a$$b%%c\\"d"')

if __name__ == '__main__':
    unittest.main()
