import {execa} from 'execa'
import * as fs from 'fs'
import * as crypto from 'crypto'


const targets = [
    ['darwin', 'amd64'],
    ['darwin', 'arm64'],

    ['linux', 'amd64'],
    ['linux', 'arm64'],
]

const build = async (conf) => {
    const [platform, arch] = conf
    const name = `fuse-${platform}-${arch}`
    const outFile = `output/${name}`
    await execa('go', ['build', '-o', outFile], {env: {GOOS: conf[0], GOARCH: conf[1]}})
    const checksum = crypto.createHash('sha512')
    checksum.update(fs.readFileSync(outFile))
    return [name, checksum.digest('hex')]
}


if (fs.existsSync('output')) {
    fs.rmSync('output', {recursive: true})
}
fs.mkdirSync('output')


const metadatas = Object.fromEntries(await Promise.all(targets.map(t => build(t))))

fs.writeFileSync('output/checksums.json', JSON.stringify(metadatas))
console.log(metadatas)