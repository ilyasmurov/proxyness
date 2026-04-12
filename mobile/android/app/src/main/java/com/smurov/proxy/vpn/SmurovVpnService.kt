package com.smurov.proxy.vpn

import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.util.Log
import java.io.FileInputStream
import java.io.FileOutputStream
import java.nio.ByteBuffer
import javax.crypto.Mac
import javax.crypto.spec.SecretKeySpec
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocket

class SmurovVpnService : VpnService() {

    companion object {
        const val TAG = "SmurovVPN"
        const val ACTION_CONNECT = "com.smurov.proxy.vpn.CONNECT"
        const val ACTION_DISCONNECT = "com.smurov.proxy.vpn.DISCONNECT"
        const val EXTRA_SERVER = "server"
        const val EXTRA_KEY = "key"

        var isRunning = false
            private set
        var lastError: String? = null
            private set
    }

    private var vpnInterface: ParcelFileDescriptor? = null
    private var tlsSocket: SSLSocket? = null
    @Volatile private var running = false

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_CONNECT -> {
                val server = intent.getStringExtra(EXTRA_SERVER) ?: return START_NOT_STICKY
                val key = intent.getStringExtra(EXTRA_KEY) ?: return START_NOT_STICKY
                Thread { connect(server, key) }.start()
            }
            ACTION_DISCONNECT -> disconnect()
        }
        return START_STICKY
    }

    private fun connect(server: String, key: String) {
        try {
            lastError = null
            isRunning = false

            val parts = server.split(":")
            val host = parts[0]
            val port = if (parts.size > 1) parts[1].toInt() else 443

            val sslContext = SSLContext.getInstance("TLS")
            sslContext.init(null, null, null)
            val factory = sslContext.socketFactory
            val socket = factory.createSocket() as SSLSocket
            protect(socket)

            socket.connect(java.net.InetSocketAddress(host, port), 10000)
            socket.soTimeout = 30000
            tlsSocket = socket

            val out = socket.outputStream
            val inp = socket.inputStream

            out.write(byteArrayOf(0x01))

            val keyBytes = hexToBytes(key)
            val authMsg = ByteArray(41)
            authMsg[0] = 0x01
            val timestamp = System.currentTimeMillis() / 1000
            ByteBuffer.wrap(authMsg, 1, 8).putLong(timestamp)
            val mac = Mac.getInstance("HmacSHA256")
            mac.init(SecretKeySpec(keyBytes, "HmacSHA256"))
            mac.update(authMsg, 1, 8)
            val hmac = mac.doFinal()
            System.arraycopy(hmac, 0, authMsg, 9, 32)
            out.write(authMsg)
            out.flush()

            val result = inp.read()
            if (result != 0x01) {
                lastError = "Auth rejected by server"
                socket.close()
                return
            }

            Log.i(TAG, "Authenticated with proxy server")

            val builder = Builder()
                .setSession("SmurovProxy")
                .addAddress("10.0.0.2", 32)
                .addRoute("0.0.0.0", 0)
                .addDnsServer("8.8.8.8")
                .addDnsServer("8.8.4.4")
                .setMtu(1400)
                .setBlocking(true)

            vpnInterface = builder.establish()
            if (vpnInterface == null) {
                lastError = "Failed to establish VPN interface"
                socket.close()
                return
            }

            running = true
            isRunning = true
            Log.i(TAG, "VPN tunnel established")

            val tunIn = FileInputStream(vpnInterface!!.fileDescriptor)
            val tunOut = FileOutputStream(vpnInterface!!.fileDescriptor)

            val readerThread = Thread {
                val buf = ByteArray(1500)
                val lenBuf = ByteArray(2)
                try {
                    while (running) {
                        val n = tunIn.read(buf)
                        if (n > 0) {
                            ByteBuffer.wrap(lenBuf).putShort(n.toShort())
                            synchronized(out) {
                                out.write(lenBuf)
                                out.write(buf, 0, n)
                                out.flush()
                            }
                        }
                    }
                } catch (e: Exception) {
                    if (running) Log.e(TAG, "TUN reader error", e)
                }
            }

            val writerThread = Thread {
                val lenBuf = ByteArray(2)
                try {
                    while (running) {
                        readFully(inp, lenBuf)
                        val pktLen = ByteBuffer.wrap(lenBuf).short.toInt() and 0xFFFF
                        val pkt = ByteArray(pktLen)
                        readFully(inp, pkt)
                        tunOut.write(pkt)
                    }
                } catch (e: Exception) {
                    if (running) Log.e(TAG, "TUN writer error", e)
                }
            }

            readerThread.start()
            writerThread.start()
            readerThread.join()
            writerThread.join()

        } catch (e: Exception) {
            Log.e(TAG, "VPN connect failed", e)
            lastError = e.message
        } finally {
            cleanup()
        }
    }

    private fun disconnect() {
        running = false
        cleanup()
    }

    private fun cleanup() {
        isRunning = false
        running = false
        try { vpnInterface?.close() } catch (_: Exception) {}
        try { tlsSocket?.close() } catch (_: Exception) {}
        vpnInterface = null
        tlsSocket = null
        stopSelf()
    }

    private fun readFully(inp: java.io.InputStream, buf: ByteArray) {
        var off = 0
        while (off < buf.size) {
            val n = inp.read(buf, off, buf.size - off)
            if (n < 0) throw java.io.IOException("EOF")
            off += n
        }
    }

    private fun hexToBytes(hex: String): ByteArray {
        val len = hex.length / 2
        val data = ByteArray(len)
        for (i in 0 until len) {
            data[i] = ((Character.digit(hex[i * 2], 16) shl 4) +
                    Character.digit(hex[i * 2 + 1], 16)).toByte()
        }
        return data
    }

    override fun onDestroy() {
        disconnect()
        super.onDestroy()
    }
}
