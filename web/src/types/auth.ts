export type ConnectionStatus = 'connected' | 'disconnected' | 'connecting' | 'error';

/** Why the management API could not be reached: access refused, or no usable response. */
export type ConnectionFailure = 'forbidden' | 'unreachable';
