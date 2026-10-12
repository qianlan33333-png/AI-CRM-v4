import fs from 'node:fs/promises';
import path from 'node:path';

// Match one UI-created blob to its Browser download GUID. Human-readable
// suggested filenames are metadata, not a promise about the on-disk name.
export function createCSVDownloadTracker(directory) {
  const starts = [];
  const progress = new Map();
  return {
    async configure(call) {
      await call('Browser.setDownloadBehavior', {
        behavior: 'allowAndName', downloadPath: directory, eventsEnabled: true,
      });
    },
    observe(message) {
      if (message.method === 'Browser.downloadWillBegin') starts.push({ ...message.params });
      if (message.method === 'Browser.downloadProgress') progress.set(message.params.guid, { ...message.params });
    },
    async waitFor({ url, suggestedFilename }, timeoutMs = 5000) {
      if (typeof url !== 'string' || !url.startsWith('blob:') ||
          typeof suggestedFilename !== 'string' || !suggestedFilename.endsWith('.csv')) {
        throw new Error('Expected the current CSV anchor blob URL and filename');
      }
      const deadline = Date.now() + timeoutMs;
      let lastState = 'no matching downloadWillBegin';
      do {
        const matching = starts.filter(item => item.url === url && item.suggestedFilename === suggestedFilename);
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
