// Secure Enclave bridge of the enclave package (built by scripts/build-sebridge.sh, linked with the sebridge tag).
// Only the key work happens here: the key is created in the Secure Enclave with a user presence requirement, and
// its key agreement (Touch ID or the account password) returns the raw shared secret; sealing is done in Go.
import CryptoKit
import Foundation
import LocalAuthentication

@_cdecl("webda_se_available")
public func webdaSEAvailable() -> Int32 { SecureEnclave.isAvailable ? 1 : 0 }

/// Creates a key agreement key usable only after user presence; writes its wrapped blob (only this Secure Enclave
/// can use it) and its public key (X9.63, 65 bytes). Returns the blob length, or a negative error.
@_cdecl("webda_se_create")
public func webdaSECreate(_ blob: UnsafeMutablePointer<UInt8>, _ blobCap: Int32,
                          _ pub: UnsafeMutablePointer<UInt8>) -> Int32 {
    var err: Unmanaged<CFError>?
    guard let ac = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
                                                   [.privateKeyUsage, .userPresence], &err) else { return -1 }
    guard let key = try? SecureEnclave.P256.KeyAgreement.PrivateKey(accessControl: ac) else { return -2 }
    let data = key.dataRepresentation
    let p = key.publicKey.x963Representation
    guard data.count <= Int(blobCap), p.count == 65 else { return -3 }
    data.copyBytes(to: blob, count: data.count)
    p.copyBytes(to: pub, count: 65)
    return Int32(data.count)
}

/// Key agreement of the wrapped key with peer (X9.63, 65 bytes): prompts for user presence with reason; writes the
/// 32-byte shared secret. Returns 0, 1 when the person cancels or fails, or a negative error.
@_cdecl("webda_se_agree")
public func webdaSEAgree(_ blob: UnsafePointer<UInt8>, _ blobLen: Int32, _ peer: UnsafePointer<UInt8>,
                         _ reason: UnsafePointer<CChar>, _ out: UnsafeMutablePointer<UInt8>) -> Int32 {
    let ctx = LAContext()
    ctx.localizedReason = String(cString: reason)
    do {
        let key = try SecureEnclave.P256.KeyAgreement.PrivateKey(
            dataRepresentation: Data(bytes: blob, count: Int(blobLen)), authenticationContext: ctx)
        let peerKey = try P256.KeyAgreement.PublicKey(x963Representation: Data(bytes: peer, count: 65))
        let shared = try key.sharedSecretFromKeyAgreement(with: peerKey)
        return shared.withUnsafeBytes { raw -> Int32 in
            guard raw.count == 32 else { return -4 }
            raw.copyBytes(to: UnsafeMutableRawBufferPointer(start: out, count: 32))
            return 0
        }
    } catch let e as LAError {
        return e.code == .userCancel || e.code == .authenticationFailed || e.code == .systemCancel ? 1 : -5
    } catch {
        let ns = error as NSError
        return ns.domain == LAErrorDomain ? 1 : -6
    }
}
