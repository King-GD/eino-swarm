import { ImageGenerationError, maxGeneratedImageBytes, type GeneratedImageMIME } from "./gateway-image"

const name = "zwai.phone.generatedImages"
const images = "images"

export type DirectImageRef = { id: string; name: string; mime: GeneratedImageMIME; width: number; height: number }

function openImages(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(name, 1)
    request.onupgradeneeded = () => { request.result.createObjectStore(images) }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
}

async function imageTransaction<T>(mode: IDBTransactionMode, run: (rows: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const db = await openImages()
  try {
    return await new Promise((resolve, reject) => {
      const tx = db.transaction(images, mode)
      const request = run(tx.objectStore(images))
      tx.oncomplete = () => resolve(request.result)
      tx.onabort = () => reject(tx.error ?? request.error)
    })
  } finally { db.close() }
}

export async function saveDirectImage(ref: DirectImageRef, blob: Blob) {
  if (blob.size <= 0 || blob.size > maxGeneratedImageBytes || blob.type !== ref.mime) throw new ImageGenerationError("image_invalid_response")
  try {
    // Private WebKit stores can reject Blob writes. Structured-clone the exact
    // bytes and reconstruct the MIME-bearing Blob only when the transcript reads it.
    const bytes = await blob.arrayBuffer()
    await imageTransaction("readwrite", rows => rows.add(bytes, ref.id))
  } catch { throw new ImageGenerationError("image_storage_failed") }
}

export async function loadDirectImage(ref: DirectImageRef, signal: AbortSignal): Promise<Blob> {
  if (signal.aborted) throw new ImageGenerationError("image_cancelled")
  const bytes: unknown = await imageTransaction("readonly", rows => rows.get(ref.id))
  if (signal.aborted) throw new ImageGenerationError("image_cancelled")
  if (!(bytes instanceof ArrayBuffer) || bytes.byteLength <= 0 || bytes.byteLength > maxGeneratedImageBytes) {
    throw new ImageGenerationError("image_storage_failed")
  }
  return new Blob([bytes], { type: ref.mime })
}

export async function removeDirectImage(id: string) {
  await imageTransaction("readwrite", rows => rows.delete(id))
}
