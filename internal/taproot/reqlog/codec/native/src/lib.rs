//! C ABI over `lepton_jpeg`, `preflate-rs`, and the zstd reference encoder for
//! taproot's request-log blob codecs. The Go package one directory up
//! (`codec_native.go`) is the only caller.
//!
//! Every entry point catches panics and returns a status code. Output buffers
//! are allocated here and must be released with `tpc_free`. Nothing here keeps
//! global state, so all entry points are safe to call concurrently; Lepton and
//! zstd run single-threaded because Go owns the parallelism.

use std::io::Cursor;
use std::panic::{AssertUnwindSafe, catch_unwind};

use lepton_jpeg::{EnabledFeatures, SingleThreadPool};
use preflate_rs::{PreflateConfig, PreflateStreamProcessor, recreate_whole_deflate_stream};
use zstd::zstd_safe::CParameter;

/// The call succeeded and its outputs are set.
pub const TPC_OK: i32 = 0;
/// The codec rejected the input.
pub const TPC_ERR: i32 = 1;
/// The codec panicked; the panic was caught.
pub const TPC_PANIC: i32 = 2;
/// A pointer argument was null.
pub const TPC_BAD_ARG: i32 = 3;
/// The deflate stream ended before its final block.
pub const TPC_INCOMPLETE: i32 = 4;

/// A byte buffer owned by this library. `ptr` is null when `len` is 0.
#[repr(C)]
pub struct TpcBuf {
    pub ptr: *mut u8,
    pub len: usize,
}

impl TpcBuf {
    fn set(&mut self, v: Vec<u8>) {
        if v.is_empty() {
            self.ptr = std::ptr::null_mut();
            self.len = 0;
            return;
        }
        let b = v.into_boxed_slice();
        self.len = b.len();
        self.ptr = Box::into_raw(b).cast::<u8>();
    }
}

/// Builds a slice from a caller pointer, treating a null or empty input as `&[]`.
///
/// # Safety
/// When `len > 0`, `ptr` must point to `len` readable bytes that stay valid
/// for the call.
unsafe fn input<'a>(ptr: *const u8, len: usize) -> &'a [u8] {
    if ptr.is_null() || len == 0 {
        &[]
    } else {
        // SAFETY: guaranteed by the caller.
        unsafe { std::slice::from_raw_parts(ptr, len) }
    }
}

fn guarded(f: impl FnOnce() -> i32) -> i32 {
    catch_unwind(AssertUnwindSafe(f)).unwrap_or(TPC_PANIC)
}

/// Lepton-encodes a JPEG into `out`. The caller verifies the round trip.
///
/// # Safety
/// `data` must point to `len` readable bytes and `out` must be a valid,
/// writable `TpcBuf`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn tpc_lepton_encode(data: *const u8, len: usize, out: *mut TpcBuf) -> i32 {
    if out.is_null() {
        return TPC_BAD_ARG;
    }
    // SAFETY: guaranteed by the caller.
    let (data, out) = unsafe { (input(data, len), &mut *out) };
    guarded(|| {
        let mut reader = Cursor::new(data);
        let mut encoded = Cursor::new(Vec::with_capacity(data.len()));
        match lepton_jpeg::encode_lepton(
            &mut reader,
            &mut encoded,
            &EnabledFeatures::compat_lepton_vector_write(),
            &SingleThreadPool::default(),
        ) {
            Ok(_) => {
                out.set(encoded.into_inner());
                TPC_OK
            }
            Err(_) => TPC_ERR,
        }
    })
}

/// Decodes Lepton data into `out`. `max_jpeg_len` bounds the JPEG size the
/// Lepton header may claim, so corrupt input cannot request a huge buffer.
///
/// # Safety
/// `data` must point to `len` readable bytes and `out` must be a valid,
/// writable `TpcBuf`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn tpc_lepton_decode(
    data: *const u8,
    len: usize,
    max_jpeg_len: u32,
    out: *mut TpcBuf,
) -> i32 {
    if out.is_null() {
        return TPC_BAD_ARG;
    }
    // SAFETY: guaranteed by the caller.
    let (data, out) = unsafe { (input(data, len), &mut *out) };
    guarded(|| {
        let mut features = EnabledFeatures::compat_lepton_vector_read();
        features.max_jpeg_file_size = max_jpeg_len;
        let mut reader = Cursor::new(data);
        let mut decoded = Vec::with_capacity(max_jpeg_len as usize);
        match lepton_jpeg::decode_lepton(
            &mut reader,
            &mut decoded,
            &features,
            &SingleThreadPool::default(),
        ) {
            Ok(_) => {
                out.set(decoded);
                TPC_OK
            }
            Err(_) => TPC_ERR,
        }
    })
}

/// Splits a raw deflate stream into its plaintext and the preflate corrections
/// that recreate it. `consumed` receives the length of the deflate stream,
/// which may be shorter than `len` when other bytes follow it.
///
/// # Safety
/// `data` must point to `len` readable bytes; `plain`, `corrections`, and
/// `consumed` must be valid and writable.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn tpc_deflate_unpack(
    data: *const u8,
    len: usize,
    plain_limit: usize,
    plain: *mut TpcBuf,
    corrections: *mut TpcBuf,
    consumed: *mut usize,
) -> i32 {
    if plain.is_null() || corrections.is_null() || consumed.is_null() {
        return TPC_BAD_ARG;
    }
    // SAFETY: guaranteed by the caller.
    let (data, plain, corrections, consumed) = unsafe {
        (
            input(data, len),
            &mut *plain,
            &mut *corrections,
            &mut *consumed,
        )
    };
    guarded(|| {
        let config = PreflateConfig {
            plain_text_limit: plain_limit,
            // The Go caller rebuilds the whole file and compares it.
            verify_compression: false,
            ..PreflateConfig::default()
        };
        let mut processor = PreflateStreamProcessor::new(&config);
        let chunk = match processor.decompress(data) {
            Ok(c) => c,
            Err(_) => return TPC_ERR,
        };
        if !processor.is_done() {
            return TPC_INCOMPLETE;
        }
        *consumed = chunk.compressed_size;
        plain.set(processor.detach_plain_text().text().to_vec());
        corrections.set(chunk.corrections);
        TPC_OK
    })
}

/// Recreates the deflate stream from the plaintext and corrections that
/// `tpc_deflate_unpack` produced.
///
/// # Safety
/// `plain` and `corrections` must point to their stated number of readable
/// bytes and `out` must be a valid, writable `TpcBuf`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn tpc_deflate_pack(
    plain: *const u8,
    plain_len: usize,
    corrections: *const u8,
    corrections_len: usize,
    out: *mut TpcBuf,
) -> i32 {
    if out.is_null() {
        return TPC_BAD_ARG;
    }
    // SAFETY: guaranteed by the caller.
    let (plain, corrections, out) = unsafe {
        (
            input(plain, plain_len),
            input(corrections, corrections_len),
            &mut *out,
        )
    };
    guarded(|| match recreate_whole_deflate_stream(plain, corrections) {
        Ok(v) => {
            out.set(v);
            TPC_OK
        }
        Err(_) => TPC_ERR,
    })
}

/// Compresses `data` into one zstd frame at `level`, with the content size and
/// a checksum in the frame. Any conforming decoder reads it.
///
/// # Safety
/// `data` must point to `len` readable bytes and `out` must be a valid,
/// writable `TpcBuf`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn tpc_zstd_compress(
    data: *const u8,
    len: usize,
    level: i32,
    out: *mut TpcBuf,
) -> i32 {
    if out.is_null() {
        return TPC_BAD_ARG;
    }
    // SAFETY: guaranteed by the caller.
    let (data, out) = unsafe { (input(data, len), &mut *out) };
    guarded(|| {
        let frame = zstd::bulk::Compressor::new(level).and_then(|mut c| {
            c.set_parameter(CParameter::ChecksumFlag(true))?;
            c.compress(data)
        });
        match frame {
            Ok(v) => {
                out.set(v);
                TPC_OK
            }
            Err(_) => TPC_ERR,
        }
    })
}

/// Releases a buffer this library returned and resets it to empty.
///
/// # Safety
/// `buf` must be null or a `TpcBuf` filled by this library and not yet freed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn tpc_free(buf: *mut TpcBuf) {
    if buf.is_null() {
        return;
    }
    // SAFETY: guaranteed by the caller.
    let buf = unsafe { &mut *buf };
    if !buf.ptr.is_null() {
        // SAFETY: ptr and len came from Box::into_raw on a boxed slice in TpcBuf::set.
        drop(unsafe { Box::from_raw(std::ptr::slice_from_raw_parts_mut(buf.ptr, buf.len)) });
    }
    buf.ptr = std::ptr::null_mut();
    buf.len = 0;
}
