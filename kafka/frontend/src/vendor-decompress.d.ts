/**
 * Ambient 类型声明：snappyjs / lz4js 均为无类型定义的 CJS 包（MIT / ISC），
 * 这里只声明消息详情二次解码实际用到的两个 API；fzstd 自带 d.ts 无需声明。
 */
declare module "snappyjs" {
  export function compress(input: Uint8Array): Uint8Array;
  export function uncompress(compressed: Uint8Array): Uint8Array;
}

declare module "lz4js" {
  export function compress(input: Uint8Array): Uint8Array;
  export function decompress(input: Uint8Array): Uint8Array;
}
