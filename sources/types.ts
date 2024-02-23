

export interface FuseNode {
    // name: string
    type: 'dir' | 'zip'
    zipPath?: string
    ch?: Record<string, FuseNode>
}



export interface FuseData {
    roots: Record<string, FuseNode>
}