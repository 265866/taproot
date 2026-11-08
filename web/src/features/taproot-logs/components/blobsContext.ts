import { createContext } from 'react';
import type { Blobs } from '../model/blobs';

/** The placeholders of the open record, so a JSON string can show its payload inline. */
export const BlobsContext = createContext<Blobs>({});
