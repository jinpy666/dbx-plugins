"""Offline regression tests for smoke result attribution; no sidecar is started."""

import unittest

from smoke_test import RESULTS, SidecarError, SkipScenario, scenario


class ScenarioTests(unittest.TestCase):
    def setUp(self):
        self.previous = RESULTS[:]
        RESULTS.clear()

    def tearDown(self):
        RESULTS[:] = self.previous

    def test_success_runs_once_and_preserves_args_and_optional_feature_note(self):
        calls = []

        @scenario("S-test", "single execution")
        def body(value, *, optional):
            calls.append((value, optional))
            if len(calls) > 1:
                raise AssertionError("write scenario ran twice")
            return "optional overlay assertion skipped"

        body(42, optional=True)
        self.assertEqual(calls, [(42, True)])
        self.assertEqual(len(RESULTS), 1)
        self.assertEqual(vars(RESULTS[0]), {
            "no": "S-test", "name": "single execution", "status": "PASS",
            "detail": "optional overlay assertion skipped",
        })

    def test_success_without_note_has_empty_detail(self):
        scenario("S-test", "no note")(lambda: None)()
        self.assertEqual((RESULTS[0].status, RESULTS[0].detail), ("PASS", ""))

    def test_failures_and_skips_are_attributed_once(self):
        cases = [
            (SkipScenario("missing fixture"), "SKIP", "missing fixture"),
            (SidecarError("unsupported", -32601), "SKIP", "backend not implemented: unsupported"),
            (SidecarError("method not registered"), "SKIP", "backend not implemented: method not registered"),
            (SidecarError("access denied", -32000), "FAIL", "access denied"),
            (AssertionError("read-back differs"), "FAIL", "read-back differs"),
            (RuntimeError("transport closed"), "FAIL", "unexpected: transport closed"),
        ]
        for error, status, detail in cases:
            with self.subTest(error=repr(error)):
                RESULTS.clear()
                calls = []

                def body():
                    calls.append(True)
                    raise error

                scenario("S-error", "failure attribution")(body)()
                self.assertEqual(calls, [True])
                self.assertEqual(len(RESULTS), 1)
                self.assertEqual(vars(RESULTS[0]), {
                    "no": "S-error", "name": "failure attribution",
                    "status": status, "detail": detail,
                })


if __name__ == "__main__":
    unittest.main()
