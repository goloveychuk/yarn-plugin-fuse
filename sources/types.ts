

export interface FuseNode {
    // name: string
    target?: string
    linkType: 'HARD' | 'SOFT'
    children: Record<string, FuseNode>
}



export interface FuseData {
    roots: Record<string, FuseNode>
}