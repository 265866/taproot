export type ConnectionStatus = 'connected' | 'disconnected' | 'connecting' | 'error';

/**
 * Why the management API could not be reached: access refused, a tailnet device without Admin,
 * or no usable response.
 */
export type ConnectionFailure = 'forbidden' | 'not_admin' | 'unreachable';

/** The refused device, as the access guard names it in a `not_admin` denial. */
export interface DeniedDevice {
  /** The device's name, such as alice-work-laptop. */
  node: string;
  nodeId: string;
  login: string;
  /** Whether the device has Proxy. */
  proxy: boolean;
}
