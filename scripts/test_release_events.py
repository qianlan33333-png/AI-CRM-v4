import copy
import sys
import unittest
from pathlib import Path
sys.path.insert(0, str(Path(__file__).parent))
from release_events import append, claim, finish, replay, acknowledge

class ReleaseEventsTest(unittest.TestCase):
    def setUp(self):
        self.state = {'schema': 2, 'events': [], 'items': [], 'returns': []}
        self.value = {'event_type': 'handoff_ready', 'origin_thread_id': 'origin', 'destination_thread_id': 'coordinator', 'candidate_id': 'c1', 'commit_sha': 'a'*40, 'tree_sha': 'b'*40}
    def test_idempotent_and_immutable_payload(self):
        first = append(self.state, self.value)
        self.assertEqual(first['payload']['event_id'], append(self.state, self.value)['payload']['event_id'])
        altered = dict(self.value, destination_thread_id='other')
        with self.assertRaises(ValueError): append(self.state, altered)
        self.assertEqual(len(self.state['events']), 1)
    def test_claim_lease_token_backoff_and_dead_letter(self):
        event = append(self.state, self.value); event_id = event['payload']['event_id']; at = event['delivery']['next_attempt_at']
        for attempt in range(5):
            claimed = claim(self.state, event_id, at=at); token = claimed['delivery']['lease_token']
            with self.assertRaises(ValueError): claim(self.state, event_id, at=at)
            with self.assertRaises(ValueError): finish(self.state, event_id, 'sent', 'wrong', at=at)
            finish(self.state, event_id, 'failed', token, 'offline', at=at)
            at = event['delivery']['next_attempt_at']
            if attempt < 4:
                with self.assertRaises(ValueError): claim(self.state, event_id, at=at-1)
        self.assertEqual(event['delivery']['status'], 'dead_letter')
        replay(self.state, event_id)
        self.assertEqual(event['delivery']['status'], 'pending')
    def test_expired_lease_reclaimed_old_token_rejected(self):
        event = append(self.state, self.value); event_id = event['payload']['event_id']; at = event['delivery']['next_attempt_at']
        first = claim(self.state, event_id, at=at)['delivery']['lease_token']
        second = claim(self.state, event_id, at=at+301)['delivery']['lease_token']
        self.assertNotEqual(first, second)
        with self.assertRaises(ValueError): finish(self.state, event_id, 'sent', first, at=at+301)
        finish(self.state, event_id, 'sent', second, at=at+301)
        self.assertEqual(event['delivery']['status'], 'sent')
    def test_crashed_sender_has_bounded_leases_and_replay_audit(self):
        event = append(self.state, self.value); event_id = event['payload']['event_id']; at = event['delivery']['next_attempt_at']
        for _ in range(5):
            claim(self.state, event_id, at=at)
            at += 301
        exhausted = claim(self.state, event_id, at=at)
        self.assertEqual(exhausted['delivery']['status'], 'dead_letter')
        self.assertEqual(exhausted['delivery']['attempt'], 5)
        self.assertEqual(exhausted['delivery']['lifetime_attempt'], 5)
        self.assertEqual(len(exhausted['delivery']['history']), 6)
        replay(self.state, event_id)
        self.assertEqual(exhausted['delivery']['lifetime_attempt'], 5)
        self.assertEqual(exhausted['delivery']['history'][-1]['result'], 'manual_replay')
    def test_transport_sent_does_not_mean_origin_processed(self):
        event = append(self.state, self.value); event_id = event['payload']['event_id']; at = event['delivery']['next_attempt_at']
        token = claim(self.state, event_id, at=at)['delivery']['lease_token']
        finish(self.state, event_id, 'sent', token, at=at)
        self.assertEqual(event['processing']['status'], 'unacknowledged')
        with self.assertRaises(ValueError): acknowledge(self.state, event_id, 'wrong-thread', 'received')
        acknowledge(self.state, event_id, 'coordinator', 'received')
        acknowledge(self.state, event_id, 'coordinator', 'received')
        self.assertEqual(event['processing']['status'], 'acknowledged')
        self.assertEqual(len(event['processing']['observations']), 1)
if __name__ == '__main__': unittest.main()
