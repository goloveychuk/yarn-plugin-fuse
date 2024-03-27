import * as path from 'path';
import * as fs from 'fs/promises';
import * as crypto from 'crypto';
import { spawn } from 'child_process';
import { pipeline } from 'stream/promises';
import { setInterval } from 'timers/promises';
import { xfs, ppath, PortablePath } from '@yarnpkg/fslib';
import { getExecFileName } from '../utils.mjs';
import metadata from '../fuse/output/metadata.json';
import { fileURLToPath } from 'url';
import * as os from 'os';


async function checkChecksum(p: string, checksum: string) {
  const hash = crypto.createHash('sha512');
  const stream = await fs.open(p, 'r');
  await pipeline(stream.createReadStream(), hash);
  const hashRes = hash.digest('hex');
  if (hashRes !== checksum) {
    throw new Error(`Checksum mismatch for ${p}`);
  }
}
const MAGIC_PATH = '.00unmount';

export async function unmountFuse(nmPath: PortablePath) {
  const p = ppath.join(nmPath, MAGIC_PATH);
  if (await xfs.existsPromise(p)) {
    await xfs.removePromise(p, {});
  }
}

async function waitToMount(nmPath: PortablePath) {
  const p = ppath.join(nmPath, MAGIC_PATH);
  const deadLine = Date.now() + 10_000;
  for await (const _ of setInterval(300)) {
    if (Date.now() > deadLine) {
      throw new Error('Timeout waiting for fuse to mount');
    }
    if (await xfs.existsPromise(p)) {
      break;
    }
  }
}

type Fetcher = (url: string) => Promise<NodeJS.ReadableStream>

async function downloadFile(fetcher: Fetcher, url: string) {
  const tmpPath = path.join(os.tmpdir(), crypto.randomUUID());
  const stream = await fetcher(url)
  const handle = await fs.open(tmpPath, 'w', 0o700)
  await pipeline(stream, handle.createWriteStream());
  await handle.close();
  return tmpPath;
}

async function downloadFileOrCache(fetcher: Fetcher, url: string, key: string): Promise<string> {
  const resultPath = path.join(os.tmpdir(), key);
  if (await (fs.stat(resultPath).catch(() => false))) {
    return resultPath;
  }
  const newPath = await downloadFile(fetcher, url);
  await fs.rename(newPath, resultPath);
  return resultPath
}

export async function runFuse({ fetcher, nmPath, projectRoot, confPath }: { fetcher: Fetcher, nmPath: PortablePath, projectRoot: string, confPath: string }) {
  const info = os.userInfo();
  const name = getExecFileName() as keyof typeof metadata;
  const meta = metadata[name];
  if (!meta) {
    throw new Error(`No checksum found for ${name}`);
  }
  const filePath = new URL(meta.path);
  let realFilePath: string;
  if (filePath.protocol === 'file:') {
    realFilePath = fileURLToPath(filePath);
  } else {
    realFilePath = await downloadFileOrCache(fetcher, filePath.href, `${info.uid}-${meta.checksum}`);
  }
  await checkChecksum(realFilePath, meta.checksum);
  const workdir = path.join(projectRoot, '.fuse-workdir');
  const child = spawn('sudo', [realFilePath, '-workdir', workdir, '-uid', String(info.uid), '-gid', String(info.gid), confPath], {
    detached: true,
  });
  console.log("PID", child.pid)

  child.stderr.pipe(process.stderr)
  child.stdout.pipe(process.stdout)
  
  const clean = () => {
    child.stdout.destroy()
    child.stderr.destroy()
  }

  child.unref()  
  await waitToMount(nmPath);
  clean()
  return clean
}
