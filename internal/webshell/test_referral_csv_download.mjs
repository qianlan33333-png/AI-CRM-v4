import fs from 'node:fs/promises';
import path from 'node:path';

// Match one UI-created blob to its Browser download GUID. Human-readable
// suggested filenames are metadata, not a promise about the on-disk name.
export function createCSVDownloadTracker(directory) {
  const starts = [];
  const progress = new Map();
  let progressEvents = 0;
  return {
    async configure(call) {
      await call('Browser.setDownloadBehavior', {
        behavior: 'allowAndName', downloadPath: directory, eventsEnabled: true,
      });
    },
    observe(message) {
      if (message.method === 'Browser.downloadWillBegin') starts.push({ ...message.params });
      if (message.method === 'Browser.downloadProgress') {
        progressEvents++;
        progress.set(message.params.guid, { ...message.params });
      }
    },
    diagnostics({ url, suggestedFilename } = {}) {
      const matching = starts.filter(item => item.url === url);
      return {
        begin_events: starts.length, progress_events: progressEvents,
        matching_begin_events: matching.length,
        expected_url_kind: typeof url === 'string' && url.startsWith('blob:') ? 'blob' : 'not_observed',
        // Matching booleans reveal the difference between no events and
        // unrelated events without saving raw URLs, names, headers or cookies.
        observed_begins: starts.map(item => ({ url_matches_expected: item.url === url,
          filename_matches_expected: item.suggestedFilename === suggestedFilename,
          guid_is_safe: typeof item.guid === 'string' && /^[A-Za-z0-9-]+$/.test(item.guid) })),
        matching_guid_states: matching.map(item => {
          const state = progress.get(item.guid)?.state;
          return { state: ['inProgress', 'completed', 'canceled'].includes(state) ? state : 'not_observed' };
        }),
      };
    },
    async waitFor({ url, suggestedFilename }, timeoutMs = 5000) {
      if (typeof url !== 'string' || !url.startsWith('blob:')) {
        throw new Error('Expected the current CSV anchor blob URL');
      }
      const deadline = Date.now() + timeoutMs;
      let lastState = 'no matching downloadWillBegin';
      do {
        // The runner already checks the UI anchor's logical filename. Chrome's
        // suggested filename is platform metadata and must not gate ownership.
        const matching = starts.filter(item => item.url === url);
        if (matching.length > 1) throw new Error('Current CSV blob started more than one download');
        const started = matching[0];
        if (started) {
          if (typeof started.guid !== 'string' || !/^[A-Za-z0-9-]+$/.test(started.guid)) {
            throw new Error('Current CSV download GUID is not a safe file identifier');
          }
          const update = progress.get(started.guid);
          lastState = update?.state || 'waiting for this CSV GUID';
          if (update?.state === 'canceled') throw new Error('Current CSV download was canceled');
          if (update?.state === 'completed') {
            const destination = path.join(directory, started.guid);
            try {
              const root = await fs.lstat(directory);
              const stat = await fs.lstat(destination);
              if (!root.isDirectory() || root.isSymbolicLink() || !stat.isFile() || stat.isSymbolicLink()) {
                throw new Error('Current CSV download must be an isolated ordinary file');
              }
              if (update.totalBytes > 0 && stat.size !== update.totalBytes) {
                lastState = 'completed GUID file size differs from download bytes';
              } else {
                return { guid: started.guid, url, suggestedFilename, path: destination,
                  state: update.state, totalBytes: update.totalBytes, size: stat.size };
              }
            } catch (error) {
              if (error.code !== 'ENOENT') throw error;
              lastState = 'completed CSV GUID has no file yet';
            }
          }
        }
        await new Promise(resolve => setTimeout(resolve, 25));
      } while (Date.now() < deadline);
      throw new Error('Current CSV download did not finish at its GUID path: ' + lastState);
    },
  };
}
