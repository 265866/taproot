export type ConnectionStatus = 'connected' | 'disconnected' | 'connecting' | 'error';

/**
 * Why the management API could not be reached: access refused, an allowed account on a device
 * outside access.admin-nodes, or no usable response.
 */
export type ConnectionFailure = 'forbidden' | 'not_admin' | 'unreachable';
