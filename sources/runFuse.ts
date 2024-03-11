//@ts-expect-error
import checksums from '../fuse/output/checksums.json';
import * as os from 'os'
import * as path from 'path'
import * as fs from 'fs'
import * as crypto from 'crypto'
import {pipeline} from 'stream/promises'

const archMap = {
    'x64': 'amd64'
}

async function checkChecksum(p: string, checksum: string) {
    const hash = crypto.createHash('sha512');
    const stream = fs.createReadStream(p);
    await pipeline(stream, hash);
    const hashRes = hash.digest('hex');
    console.log(`Checksum for ${p} is ${hashRes}`);
    if (hashRes !== checksum) {
        throw new Error(`Checksum mismatch for ${p}`);
    }
}

export async function runFuse() {
    let arch = os.arch();
    const platform = os.platform();
    if (archMap[arch]) {
        arch = archMap[arch]
    }
    const name = `fuse-${platform}-${arch}`;
    const checksum = checksums[name];
    if (!checksum) {
        throw new Error(`No checksum found for ${name}`);
    }

    const file  = path.join('/Users/vadymh/github/yarn-plugin-fuse/fuse/output', name);
    await checkChecksum(file, checksum)
    console.log(`Running ${file}`);
}